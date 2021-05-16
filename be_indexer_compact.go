package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
	"sort"
	"strings"
)

type (
	CompactedBEIndex struct {
		indexBase
		wildcardKey     Key
		wildcardEntries Entries
		postingList     *PostingEntries
	}
)

func NewCompactedBEIndex(idGen parser.IDAllocator) BEIndex {
	index := &CompactedBEIndex{
		indexBase: indexBase{
			idAllocator: idGen,
			fieldDesc:   make(map[BEField]*FieldDesc),
			idToField:   make(map[uint64]*FieldDesc),
		},
		postingList: &PostingEntries{
			plEntries: make(map[Key]Entries, 0),
		},
	}
	wildcardDesc := index.configureField("__wildcard__", FieldOption{
		Parser: parser.CommonParser,
	})
	index.wildcardKey = NewKey(wildcardDesc.ID, 0)
	return index
}

func (bi *CompactedBEIndex) ConfigureIndexer(settings *IndexerSettings) {
	for field, option := range settings.FieldConfig {
		bi.configureField(field, option)
	}
}

func (bi *CompactedBEIndex) appendWildcardEntryID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

//newPostingEntriesIfNeeded(k int) *PostingEntries
func (bi *CompactedBEIndex) newPostingEntriesIfNeeded(k int) *PostingEntries {
	_ = k
	return bi.postingList
}

func (bi *CompactedBEIndex) completeIndex() {
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
	bi.postingList.makeEntriesSorted()
}

// parse queries value to value id list
func (bi *CompactedBEIndex) parseQueries(queries Assignments) (map[BEField][]uint64, error) {
	idAssigns := make(map[BEField][]uint64, len(queries))

	for field, values := range queries {
		if !bi.hasField(field) {
			continue
		}

		desc, ok := bi.fieldDesc[field]
		if !ok { //no such field, ignore it(ps: bz it will not match any doc)
			continue
		}

		res := make([]uint64, 0, len(values))
		for _, value := range values {
			ids, err := desc.Parser.ParseAssign(value)
			if err != nil {
				Logger.Errorf("field:%s, value:%+v can't be parsed, err:%s\n", field, value, err.Error())
				return nil, fmt.Errorf("query assign parse fail,field:%s e:%s\n", field, err.Error())
			}
			res = append(res, ids...)
		}
		idAssigns[field] = res
	}
	return idAssigns, nil
}

func (bi *CompactedBEIndex) Retrieve(queries Assignments) (result DocIDList, err error) {

	idAssigns, err := bi.parseQueries(queries)
	if err != nil {
		Logger.Errorf("invalid query assigns:%s", err.Error())
		return nil, err
	}

	ctx := &RetrieveContext{
		idAssigns: idAssigns,
	}

	plgs := make([]*FieldScannerGroup, 0, len(ctx.idAssigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewPostingList(bi.wildcardKey, bi.wildcardEntries)
		plgs = append(plgs, NewFieldPostingListGroup(pl))
	}

	for field, ids := range ctx.idAssigns {
		desc := bi.fieldDesc[field]

		pls := make([]*EntriesScanner, 0, len(ids))
		for _, id := range ids {
			key := NewKey(desc.ID, id)
			if entries := bi.postingList.getEntries(key); len(entries) > 0 {
				pls = append(pls, NewPostingList(key, entries))
			}
		}
		if len(pls) > 0 {
			plgs = append(plgs, NewFieldPostingListGroup(pls...))
		}
	}

	if len(plgs) == 0 {
		return result, nil
	}

	result = make([]DocID, 0, 128)

	SortPostingListGroups(plgs)
RETRIEVE:
	for {
		eid := plgs[0].GetCurEntryID()

		// K mean for this plgs, a doc match need k number same eid in every plg
		k := eid.GetConjID().Size()
		if k == 0 {
			k = 1
		}
		// remove finished posting list
		for plgs[len(plgs)-1].GetCurEntryID().IsNULLEntry() {
			plgs = plgs[:len(plgs)-1]
		}
		// mean any conjunction its size = k will not match, wil can fast skip to min entry that conjunction size > k
		if k > len(plgs) {
			break RETRIEVE
		}

		// k <= plgsCount
		// check whether eid  plgs[k-1].GetCurEntryID equal
		endEID := plgs[k-1].GetCurEntryID()

		nextID := endEID

		if eid == endEID {

			nextID = endEID + 1

			if eid.IsInclude() {

				result = append(result, eid.GetConjID().DocID())

			} else { //exclude

				for i := k; i < len(plgs); i++ {
					if plgs[i].GetCurConjID() != eid.GetConjID() {
						break
					}
					plgs[i].Skip(nextID)
				}
			}
		}
		// 推进游标
		for i := 0; i < k; i++ {
			plgs[i].SkipTo(nextID)
		}

		SortPostingListGroups(plgs)
	}
	return result, nil
}

func (bi *CompactedBEIndex) DumpEntriesSummary() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("wildcard entries length:%d >>>>>>\n", len(bi.wildcardEntries)))
	ues := bi.postingList
	sb.WriteString(fmt.Sprintf("postingList avgLen:%d maxLen:%d >>>>>>\n", ues.avgLen, ues.maxLen))
	return sb.String()
}

func (bi *CompactedBEIndex) DumpEntries() string {
	sb := strings.Builder{}

	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(bi.StringKey(bi.wildcardKey))
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("postingList avgLen:%d maxLen:%d >>>>>>\n",
		bi.postingList.avgLen, bi.postingList.maxLen))
	for k, entries := range bi.postingList.plEntries {
		sb.WriteString(bi.StringKey(k))
		sb.WriteString(":")
		sb.WriteString(fmt.Sprintf("%v", entries.DocString()))
		sb.WriteString("\n")
	}
	return sb.String()
}
