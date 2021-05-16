package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"sort"
	"strings"
)

type (
	SizeGroupedBEIndex struct {
		indexBase
		sizeEntries     []*PostingEntries
		wildcardKey     Key
		wildcardEntries Entries
	}
)

func NewSizeGroupedBEIndex(idGen parser.IDAllocator) BEIndex {
	index := &SizeGroupedBEIndex{
		indexBase: indexBase{
			idAllocator: idGen,
			fieldDesc:   make(map[BEField]*FieldDesc),
			idToField:   make(map[uint64]*FieldDesc),
		},
		sizeEntries: make([]*PostingEntries, 0),
	}
	wildcardDesc := index.configureField("__wildcard__", FieldOption{
		Parser: parser.CommonParser,
	})
	index.wildcardKey = NewKey(wildcardDesc.ID, 0)
	return index
}

func (bi *SizeGroupedBEIndex) ConfigureIndexer(settings *IndexerSettings) {
	for field, option := range settings.FieldConfig {
		bi.configureField(field, option)
	}
}

func (bi *SizeGroupedBEIndex) maxK() int {
	return len(bi.sizeEntries) - 1
}

//GetOrNewSizeEntries(k int) *PostingEntries
func (bi *SizeGroupedBEIndex) appendWildcardEntryID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

//GetOrNewSizeEntries(k int) *PostingEntries
func (bi *SizeGroupedBEIndex) newPostingEntriesIfNeeded(k int) *PostingEntries {
	for k >= len(bi.sizeEntries) {
		newSizeEntries := &PostingEntries{
			plEntries: make(map[Key]Entries),
		}
		bi.sizeEntries = append(bi.sizeEntries, newSizeEntries)
	}
	return bi.sizeEntries[k]
}

func (bi *SizeGroupedBEIndex) completeIndex() {
	for _, sizeEntries := range bi.sizeEntries {
		sizeEntries.makeEntriesSorted()
	}
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
}

func (bi *SizeGroupedBEIndex) getKSizeEntries(k int) *PostingEntries {
	if k >= len(bi.sizeEntries) {
		panic(fmt.Errorf("k:[%d] out of range", k))
	}
	return bi.sizeEntries[k]
}

func (bi *SizeGroupedBEIndex) initPostingList(ctx *RetrieveContext, k int) FieldScannerGroups {

	plgs := make([]*FieldScannerGroup, 0, len(ctx.idAssigns))

	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewPostingList(bi.wildcardKey, bi.wildcardEntries)
		plgs = append(plgs, NewFieldPostingListGroup(pl))
	}

	kSizeEntries := bi.getKSizeEntries(k)
	for field, ids := range ctx.idAssigns {

		desc := bi.fieldDesc[field]

		pls := make(EntriesScanners, 0, len(ids))
		for _, id := range ids {
			key := NewKey(desc.ID, id)
			if entries := kSizeEntries.getEntries(key); len(entries) > 0 {
				pls = append(pls, NewPostingList(key, entries))
			}
		}
		if len(pls) > 0 {
			plgs = append(plgs, NewFieldPostingListGroup(pls...))
		}
	}
	return plgs
}

// retrieveK MOVE TO: FieldScannerGroups ?
func (bi *SizeGroupedBEIndex) retrieveK(plgList FieldScannerGroups, k int) (result []DocID) {
	result = make([]DocID, 0, 256)

	//sort.Sort(plgList)
	SortPostingListGroups(plgList)
	for !plgList[k-1].GetCurEntryID().IsNULLEntry() {

		eid := plgList[0].GetCurEntryID()
		endEID := plgList[k-1].GetCurEntryID()

		nextID := endEID
		if eid == endEID {

			nextID = endEID + 1

			if eid.IsInclude() {
				result = append(result, eid.GetConjID().DocID())

			} else { //exclude

				for i := k; i < plgList.Len(); i++ {
					if plgList[i].GetCurConjID() != eid.GetConjID() {
						break
					}
					plgList[i].Skip(nextID)
				}
			}
		}
		// 推进游标
		for i := 0; i < k; i++ {
			plgList[i].SkipTo(nextID)
		}
		//sort.Sort(plgList)
		SortPostingListGroups(plgList)
	}
	return result
}

func (bi *SizeGroupedBEIndex) Retrieve(queries Assignments) (result DocIDList, err error) {

	idAssigns, err := bi.parseQueries(queries)
	if err != nil {
		Logger.Errorf("invalid query assigns:%s", err.Error())
		return nil, err
	}

	ctx := &RetrieveContext{
		idAssigns: idAssigns,
	}

	for k := util.MinInt(queries.Size(), bi.maxK()); k >= 0; k-- {

		plgs := bi.initPostingList(ctx, k)

		tempK := k
		if tempK == 0 {
			tempK = 1
		}
		if len(plgs) < tempK {
			continue
		}
		res := bi.retrieveK(plgs, tempK)
		result = append(result, res...)
	}
	return result, nil
}

func (bi *SizeGroupedBEIndex) DumpEntries() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(bi.StringKey(bi.wildcardKey))
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")

	for idx, ke := range bi.sizeEntries {
		sb.WriteString(fmt.Sprintf("K:%d  avgLen:%d maxLen:%d >>>>>>\n", idx, ke.avgLen, ke.maxLen))
		for k, entries := range ke.plEntries {
			sb.WriteString(bi.StringKey(k))
			sb.WriteString(":")
			sb.WriteString(fmt.Sprintf("%v", entries.DocString()))
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func (bi *SizeGroupedBEIndex) DumpEntriesSummary() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("wildcard entries length:%d >>>>>>\n", len(bi.wildcardEntries)))
	for k, kse := range bi.sizeEntries {
		sb.WriteString(fmt.Sprintf("SizeEntries k:%d avgLen:%d maxLen:%d >>>>>>\n", k, kse.avgLen, kse.maxLen))
	}
	return sb.String()
}
