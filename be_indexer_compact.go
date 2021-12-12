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
		wildcardEntries Entries
		fieldContainer  *fieldEntriesContainer
	}
)

func NewCompactedBEIndex() BEIndex {
	index := &CompactedBEIndex{
		indexBase: indexBase{
			fieldDesc: make(map[BEField]*FieldDesc),
			idToField: make(map[uint64]*FieldDesc),
		},
		fieldContainer: newFieldEntriesContainer(),
	}
	_ = index.configureField(WildcardFieldName, FieldOption{
		Parser:    parser.ParserNameCommon,
		Container: HolderNameDefault,
	})
	return index
}

func (bi *CompactedBEIndex) ConfigureIndexer(settings *IndexerSettings) {
	bi.settings = settings
	bi.fieldContainer.debugMode = bi.settings.EnableDebugMode

	for field, option := range settings.FieldConfig {
		bi.configureField(field, option)
	}
}

func (bi *CompactedBEIndex) addWildcardEID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

// newPostingEntriesIfNeeded(k int)
func (bi *CompactedBEIndex) newEntriesContainerIfNeeded(_ int) *fieldEntriesContainer {
	return bi.fieldContainer
}

func (bi *CompactedBEIndex) compileIndexer() {
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
	bi.fieldContainer.compileEntries()
}

func (bi *CompactedBEIndex) Retrieve(queries Assignments) (result DocIDList, err error) {

	ctx := &RetrieveContext{
		assigns: queries,
		option:  defaultQueryOption,
	}

	fieldScanners := make(FieldScanners, 0, len(ctx.assigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(wildcardQKey, bi.wildcardEntries)
		fieldScanners = append(fieldScanners, NewFieldScanner(pl))
	}

	var ok bool
	var desc *FieldDesc
	var holder EntriesHolder
	var entriesList CursorGroup

	for field, values := range ctx.assigns {
		if desc, ok = bi.fieldDesc[field]; !ok {
			continue
		}
		if holder = bi.fieldContainer.getFieldHolder(desc); holder == nil {
			return nil, fmt.Errorf("field:%s no holder found, what happend", field)
		}
		if entriesList, err = holder.GetEntries(desc, values); err != nil {
			return nil, err
		}
		if len(entriesList) > 0 {
			fieldScanners = append(fieldScanners, NewFieldScanner(entriesList...))
		}
	}

	if len(fieldScanners) == 0 {
		return result, nil
	}

	result = make([]DocID, 0, 128)

	fieldScanners.Sort()
RETRIEVE:
	for {
		eid := fieldScanners[0].GetCurEntryID()

		// K mean for this fieldScanners, a doc match need k number same eid in every plg
		k := eid.GetConjID().Size()
		if k == 0 {
			k = 1
		}
		// remove finished posting list
		for len(fieldScanners) > 0 && fieldScanners[len(fieldScanners)-1].GetCurEntryID().IsNULLEntry() {
			fieldScanners = fieldScanners[:len(fieldScanners)-1]
		}
		// mean any conjunction its size = k will not match, wil can fast skip to min entry that conjunction size > k
		if k > len(fieldScanners) {
			break RETRIEVE
		}

		// k <= plgsCount
		// check whether eid  fieldScanners[k-1].GetCurEntryID equal
		endEID := fieldScanners[k-1].GetCurEntryID()

		nextID := NewEntryID(endEID.GetConjID(), false)
		if endEID.GetConjID() == eid.GetConjID() {

			nextID = endEID + 1

			if eid.IsInclude() {

				result = append(result, eid.GetConjID().DocID())

			} else { //exclude

				for i := k; i < len(fieldScanners); i++ {
					if fieldScanners[i].GetCurConjID() != eid.GetConjID() {
						break
					}
					fieldScanners[i].Skip(nextID)
				}
			}
		}
		// 推进游标
		for i := 0; i < k; i++ {
			fieldScanners[i].SkipTo(nextID)
		}

		fieldScanners.Sort()
	}
	return result, nil
}

func (bi *CompactedBEIndex) DumpEntriesSummary() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("wildcard entries length:%d >>>>>>\n", len(bi.wildcardEntries)))
	return sb.String()
}

func (bi *CompactedBEIndex) DumpEntries() string {
	sb := strings.Builder{}

	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(wildcardQKey.String())
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")
	bi.fieldContainer.DumpString(&sb)
	return sb.String()
}
