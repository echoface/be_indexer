package beindexer

import (
	"be_indexer/parser"
	"be_indexer/util"
	"fmt"
	"sort"
	"strings"
)

type (
	FieldDesc struct {
		Parser parser.FieldValueParser
	}

	KSizeEntries struct {
		// posting list entries(sorted); eg: <age, 15>: []EntryID{1, 2, 3}
		plEntries map[Key]Entries
	}

	BEIndex struct {
		sizeEntries     []*KSizeEntries
		idAllocator     parser.IDAllocator
		fieldDesc       map[BEField]*FieldDesc
		fieldToID       map[BEField]uint64
		idToField       map[uint64]BEField
		wildcardKey     Key
		wildcardEntries Entries
	}
)

func NewBEIndex(idGen parser.IDAllocator) *BEIndex {
	index := &BEIndex{
		idAllocator: idGen,
		fieldDesc:   make(map[BEField]*FieldDesc),
		sizeEntries: make([]*KSizeEntries, 0),
		fieldToID:   make(map[BEField]uint64),
		idToField:   make(map[uint64]BEField),
	}
	index.fieldDesc["__inner__"] = &FieldDesc{
		Parser: parser.NewCommonStrParser(idGen),
	}
	index.wildcardKey = NewKey(index.FieldID("__wildcard__"), 0)
	return index
}

func (bi *BEIndex) FieldID(field BEField) uint64 {
	if v, ok := bi.fieldToID[field]; ok {
		return v
	}
	v := uint64(len(bi.fieldToID))
	bi.idToField[v] = field
	bi.fieldToID[field] = v
	return v
}

func (bi *BEIndex) getFieldDesc(field BEField) *FieldDesc {
	if desc, ok := bi.fieldDesc[field]; ok {
		return desc
	}
	return bi.fieldDesc["__inner__"]
}

func (bi *BEIndex) StringKey(key Key) string {
	return fmt.Sprintf("<%s,%d>", bi.getFieldFromID(key.GetFieldID()), key.GetValueID())
}

func (bi *BEIndex) getFieldFromID(v uint64) BEField {
	if field, ok := bi.idToField[v]; ok {
		return field
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

func (bi *BEIndex) NewKSizeEntriesIfNeeded(k int) *KSizeEntries {
	if k >= len(bi.sizeEntries) {
		newSizeEntries := make([]*KSizeEntries, k+1)
		for idx, _ := range newSizeEntries {
			if idx < len(bi.sizeEntries) {
				newSizeEntries[idx] = bi.sizeEntries[idx]
				continue
			}
			newSizeEntries[idx] = &KSizeEntries{
				plEntries: make(map[Key]Entries),
			}
		}
		bi.sizeEntries = newSizeEntries
	}
	return bi.sizeEntries[k]
}

func (bi *BEIndex) GetKSizeEntries(k int) *KSizeEntries {
	if k >= len(bi.sizeEntries) {
		panic(fmt.Errorf("k:[%d] out of range", k))
	}
	return bi.sizeEntries[k]
}

func (bi *BEIndex) CompleteIndex() {
	for _, sizeEntries := range bi.sizeEntries {
		sizeEntries.makeEntriesSorted()
	}
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
}

func (bi *BEIndex) parseQueryIDS(field BEField, values Values) (res []uint64, err error) {
	desc := bi.getFieldDesc(field)
	for _, value := range values {
		ids, err := desc.Parser.ParseAssign(value)
		if err != nil {
			Logger.Errorf("value can't be parsed, %+v \n", value)
			continue
		}
		res = append(res, ids...)
	}
	return res, nil
}

func (bi *BEIndex) initPostingList(k int, queries Assignments) FieldPostingListGroups {
	result := make([]*FieldPostingListGroup, 0, len(queries))
	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewPostingList(bi.wildcardKey, bi.wildcardEntries)
		result = append(result, NewFieldPostingListGroup(pl))
	}

	kSizeEntries := bi.GetKSizeEntries(k)
	for field, values := range queries {

		ids, err := bi.parseQueryIDS(field, values)
		if err != nil {
			Logger.Errorf("parse query assign fail, e:%s\n", err.Error())
			continue
		}
		fieldID := bi.FieldID(field)

		Logger.Infof("field:%s query ids:%v\n", field, ids)

		pls := PostingLists{}
		for _, id := range ids {
			key := NewKey(fieldID, id)
			entries := kSizeEntries.getEntries(key)
			if len(entries) > 0 {
				pls = append(pls, NewPostingList(key, entries))
			}
		}

		if len(pls) > 0 {
			result = append(result, NewFieldPostingListGroup(pls...))
		}
	}
	return result
}

func (bi *BEIndex) retrieveK(plgList FieldPostingListGroups, k int) (result []int32) {
	tempK := k
	for !plgList[tempK-1].GetCurEntryID().IsNULLEntry() {
		sort.Sort(plgList)

		eid := plgList[0].GetCurEntryID()
		endEID := plgList[tempK-1].GetCurEntryID()

		nextID := endEID
		if eid == endEID {

			nextID = endEID + 1

			if eid.IsInclude() {
				Logger.Infof("k:%d, retrieve doc:%d\n", tempK, eid.GetConjID().DocID())
				result = append(result, eid.GetConjID().DocID())

			} else { //exclude

				for i := tempK; i < plgList.Len(); i++ {
					if plgList[i].GetCurConjID() != eid.GetConjID() {
						break
					}
					plgList[i].SkipTo(nextID)
				}
			}
		}
		// 推进游标
		for i := 0; i < tempK; i++ {
			plgList[i].SkipTo(nextID)
		}
	}
	return result
}

func (bi *BEIndex) Retrieve(queries Assignments) (result []int32, err error) {

	k := util.MinInt(len(queries), bi.maxK())
	for ; k >= 0; k-- {

		tempK := k
		if tempK == 0 {
			tempK = 1
		}
		plgList := bi.initPostingList(k, queries)
		if len(plgList) < tempK {
			continue
		}
		plgList.Dump()
		res := bi.retrieveK(plgList, tempK)

		result = append(result, res...)
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
