package be_indexer

import (
	"fmt"
	"github.com/HuanGong/be_indexer/parser"
	"github.com/HuanGong/be_indexer/util"
	"sort"
	"strings"
)

type (
	FieldDesc struct {
		ID     uint64
		Field  BEField
		Parser parser.FieldValueParser
	}

	FieldOption struct {
		Parser string
	}

	IndexerSettings struct {
		FieldConfig map[BEField]FieldOption
	}

	KSizeEntries struct {
		// posting list entries(sorted); eg: <age, 15>: []EntryID{1, 2, 3}
		plEntries map[Key]Entries
	}

	RetrieveContext struct {
		idAssigns map[BEField][]uint64
	}

	BEIndex struct {
		sizeEntries     []*KSizeEntries
		idAllocator     parser.IDAllocator
		fieldDesc       map[BEField]*FieldDesc
		idToField       map[uint64]*FieldDesc
		wildcardKey     Key
		wildcardEntries Entries
	}
)

func NewBEIndex(idGen parser.IDAllocator) *BEIndex {
	index := &BEIndex{
		idAllocator: idGen,
		fieldDesc:   make(map[BEField]*FieldDesc),
		idToField:   make(map[uint64]*FieldDesc),
		sizeEntries: make([]*KSizeEntries, 0),
	}
	wildcardDesc := index.configureField("__wildcard__", FieldOption{
		Parser: parser.CommonParser,
	})
	index.wildcardKey = NewKey(wildcardDesc.ID, 0)
	return index
}

func (bi *BEIndex) ConfigureIndexer(settings *IndexerSettings) {
	for field, option := range settings.FieldConfig {
		bi.configureField(field, option)
	}
}

func (bi *BEIndex) configureField(field BEField, option FieldOption) *FieldDesc {
	if _, ok := bi.fieldDesc[field]; ok {
		panic(fmt.Errorf("can't configure field twice, bz field id can only match one ID"))
	}

	valueParser := parser.NewParser(option.Parser, bi.idAllocator)
	if valueParser == nil {
		valueParser = parser.NewParser(parser.CommonParser, bi.idAllocator)
	}
	desc := &FieldDesc{
		Field:  field,
		Parser: valueParser,
		ID:     uint64(len(bi.fieldDesc)),
	}

	bi.fieldDesc[field] = desc
	bi.idToField[desc.ID] = desc
	Logger.Infof("configure field:%s, fieldID:%d\n", field, desc.ID)

	return desc
}

func (bi *BEIndex) GetOrNewFieldDesc(field BEField) *FieldDesc {
	if desc, ok := bi.fieldDesc[field]; ok {
		return desc
	}
	return bi.configureField(field, FieldOption{
		Parser: parser.CommonParser,
	})
}

func (bi *BEIndex) hasField(field BEField) bool {
	_, ok := bi.fieldDesc[field]
	return ok
}

func (bi *BEIndex) StringKey(key Key) string {
	return fmt.Sprintf("<%s,%d>", bi.getFieldFromID(key.GetFieldID()), key.GetValueID())
}

func (bi *BEIndex) getFieldFromID(v uint64) BEField {
	if field, ok := bi.idToField[v]; ok {
		return field.Field
	}
	return ""
}

func (bi *BEIndex) maxK() int {
	return len(bi.sizeEntries) - 1
}

func (kse *KSizeEntries) makeEntriesSorted() {
	for _, entries := range kse.plEntries {
		sort.Sort(entries)
	}
}

func (kse *KSizeEntries) AppendEntryID(key Key, id EntryID) {
	entries, hit := kse.plEntries[key]
	if !hit {
		kse.plEntries[key] = Entries{id}
	}
	entries = append(entries, id)
	kse.plEntries[key] = entries
}

func (kse *KSizeEntries) getEntries(key Key) Entries {
	if entries, hit := kse.plEntries[key]; hit {
		return entries
	}
	return nil
}

//GetOrNewSizeEntries(k int) *KSizeEntries
func (bi *BEIndex) NewKSizeEntriesIfNeeded(k int) *KSizeEntries {
	for k >= len(bi.sizeEntries) {
		newSizeEntries := &KSizeEntries{
			plEntries: make(map[Key]Entries),
		}
		bi.sizeEntries = append(bi.sizeEntries, newSizeEntries)
	}
	return bi.sizeEntries[k]
}

func (bi *BEIndex) getKSizeEntries(k int) *KSizeEntries {
	if k >= len(bi.sizeEntries) {
		panic(fmt.Errorf("k:[%d] out of range", k))
	}
	return bi.sizeEntries[k]
}

func (bi *BEIndex) completeIndex() {
	for _, sizeEntries := range bi.sizeEntries {
		sizeEntries.makeEntriesSorted()
	}
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
}

// parse queries value to value id list
func (bi *BEIndex) parseQueries(queries Assignments) (map[BEField][]uint64, error) {
	idAssigns := make(map[BEField][]uint64, 0)

	for field, values := range queries {
		if !bi.hasField(field) {
			continue
		}

		desc, ok := bi.fieldDesc[field]
		if !ok { //no such field, ignore it(ps: bz it will not match any doc)
			continue
		}

		res := make([]uint64, 0, len(values)/2+1)
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

func (bi *BEIndex) initPostingList(ctx *RetrieveContext, k int) FieldPostingListGroups {

	plgs := make([]*FieldPostingListGroup, 0, len(ctx.idAssigns))

	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewPostingList(bi.wildcardKey, bi.wildcardEntries)
		plgs = append(plgs, NewFieldPostingListGroup(pl))
	}

	kSizeEntries := bi.getKSizeEntries(k)
	for field, ids := range ctx.idAssigns {

		desc := bi.fieldDesc[field]

		pls := PostingLists{}
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

// retrieveK MOVE TO: FieldPostingListGroups ?
func (bi *BEIndex) retrieveK(plgList FieldPostingListGroups, k int) (result []int32) {
	sort.Sort(plgList)
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
		sort.Sort(plgList)
	}
	Logger.Debugf("k:%d, retrieve docs:%+v\n", k, result)
	return result
}

func (bi *BEIndex) Retrieve(queries Assignments) (result []int32, err error) {

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
		Logger.Debugf("k:%d,res:%+v,entries:%s", k, res, plgs.Dump())
	}
	return result, nil
}

func (bi *BEIndex) DumpSizeEntries() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(bi.StringKey(bi.wildcardKey))
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")

	for idx, ke := range bi.sizeEntries {
		sb.WriteString(fmt.Sprintf("K:%d >>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n", idx))
		for k, entries := range ke.plEntries {
			sb.WriteString(bi.StringKey(k))
			sb.WriteString(":")
			sb.WriteString(fmt.Sprintf("%v", entries.DocString()))
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
