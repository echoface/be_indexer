package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"sort"
	"strings"
)

type (
	// for default Entries Holder, it can hold different field's entries,
	// but for ACMatcher or other Holder, it may only hold entries for one field
	fieldEntriesContainer struct {
		defaultHolder EntriesHolder // shared entries holder

		fieldHolder map[BEField]EntriesHolder // special field holder

		debugMode bool
	}

	SizeGroupedBEIndex struct {
		indexBase
		wildcardEntries Entries
		kSizeContainers []*fieldEntriesContainer
	}
)

func newFieldEntriesContainer() *fieldEntriesContainer {
	return &fieldEntriesContainer{
		defaultHolder: NewDefaultEntriesHolder(),
		fieldHolder:   map[BEField]EntriesHolder{},
	}
}

func (c *fieldEntriesContainer) compileEntries() {
	c.defaultHolder.EnableDebug(c.debugMode)

	c.defaultHolder.CompileEntries()

	for _, holder := range c.fieldHolder {

		holder.EnableDebug(c.debugMode)

		holder.CompileEntries()
	}
}

func (c *fieldEntriesContainer) getFieldHolder(desc *FieldDesc) EntriesHolder {
	if desc.option.Container == HolderNameDefault {
		return c.defaultHolder
	}
	if holder, ok := c.fieldHolder[desc.Field]; ok {
		return holder
	}
	return nil
}

func (c *fieldEntriesContainer) newEntriesHolder(desc *FieldDesc) EntriesHolder {
	if desc.option.Container == HolderNameDefault {
		return c.defaultHolder
	}

	if holder, ok := c.fieldHolder[desc.Field]; ok {
		return holder
	}
	holder := NewEntriesHolder(desc.option.Container)
	util.PanicIf(holder == nil, "field:%s, container:%s not found, register before using", desc.Field, desc.option.Container)

	holder.EnableDebug(c.debugMode)

	c.fieldHolder[desc.Field] = holder

	return holder
}

func (c *fieldEntriesContainer) DumpString(buf *strings.Builder) {
	c.defaultHolder.DumpEntries(buf)
	for field, holder := range c.fieldHolder {
		buf.WriteString(fmt.Sprintf("\nfield:%s entries:", field))
		holder.DumpEntries(buf)
	}
}

func NewSizeGroupedBEIndex() BEIndex {
	index := &SizeGroupedBEIndex{
		indexBase: indexBase{
			fieldDesc: make(map[BEField]*FieldDesc),
			idToField: make(map[uint64]*FieldDesc),
		},
		kSizeContainers: make([]*fieldEntriesContainer, 0),
	}
	_ = index.configureField(WildcardFieldName, FieldOption{
		Parser:    parser.ParserNameCommon,
		Container: HolderNameDefault,
	})
	return index
}

func (bi *SizeGroupedBEIndex) ConfigureIndexer(settings *IndexerSettings) {
	bi.settings = settings
	for field, option := range settings.FieldConfig {
		_ = bi.configureField(field, option)
	}
}

func (bi *SizeGroupedBEIndex) maxK() int {
	return len(bi.kSizeContainers) - 1
}

// addWildcardEID append wildcard entry id to Z set
func (bi *SizeGroupedBEIndex) addWildcardEID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

// newEntriesContainerIfNeeded(k int) *fieldEntriesContainer get a container if created, otherwise create one
func (bi *SizeGroupedBEIndex) newEntriesContainerIfNeeded(k int) *fieldEntriesContainer {
	for k >= len(bi.kSizeContainers) {
		container := newFieldEntriesContainer()
		container.debugMode = bi.settings.EnableDebugMode

		bi.kSizeContainers = append(bi.kSizeContainers, container)
	}
	return bi.kSizeContainers[k]
}

func (bi *SizeGroupedBEIndex) compileIndexer() {
	for _, sizeEntries := range bi.kSizeContainers {
		sizeEntries.compileEntries()
	}
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
}

func (bi *SizeGroupedBEIndex) getKSizeEntries(k int) *fieldEntriesContainer {
	if k >= len(bi.kSizeContainers) {
		panic(fmt.Errorf("k:[%d] out of range", k))
	}
	return bi.kSizeContainers[k]
}

func (bi *SizeGroupedBEIndex) initPlEntriesScanners(ctx *RetrieveContext, k int) (fieldScanners FieldScanners, err error) {

	fieldScanners = make(FieldScanners, 0, len(bi.fieldDesc))

	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(newQKey("__wildcard__", 0), bi.wildcardEntries)
		fieldScanners = append(fieldScanners, NewFieldScanner(pl))
	}

	kSizeContainer := bi.getKSizeEntries(k)

	var entriesList CursorGroup
	var holder EntriesHolder

	var ok bool
	var desc *FieldDesc

	for field, values := range ctx.assigns {

		if desc, ok = bi.fieldDesc[field]; !ok {
			// not recognized field, no document care about this field, ignore
			continue
		}

		if holder = kSizeContainer.getFieldHolder(desc); holder == nil {
			// Logger.Errorf("entries holder not found, field:%s", desc.Field)
			// return nil, fmt.Errorf("entries holder not found, field:%s, what happened", field)
			continue
		}

		if entriesList, err = holder.GetEntries(desc, values); err != nil {
			Logger.Errorf("fetch entries from holder fail:%s, field:%s", err.Error(), desc.Field)
			return nil, err
		}

		if len(entriesList) > 0 {
			scanner := NewFieldScanner(entriesList...)
			fieldScanners = append(fieldScanners, scanner)

			Logger.Debugf("<%s,%+v> fetch %d posting list:", desc.Field, values, len(entriesList))
		} else {
			Logger.Debugf("<%s,%+v> nothing matched from entries holder", desc.Field, values)
		}
	}
	return fieldScanners, nil
}

// retrieveK MOVE TO: FieldScanners ?
func (bi *SizeGroupedBEIndex) retrieveK(fieldScanners FieldScanners, k int) (result []DocID) {
	result = make([]DocID, 0, 256)

	//sort.Sort(fieldScanners)
	fieldScanners.Sort()
	for !fieldScanners[k-1].GetCurEntryID().IsNULLEntry() {

		eid := fieldScanners[0].GetCurEntryID()
		endEID := fieldScanners[k-1].GetCurEntryID()

		nextID := NewEntryID(endEID.GetConjID(), false)

		if eid.GetConjID() == endEID.GetConjID() {
			nextID = endEID + 1
			if eid.IsInclude() {
				result = append(result, eid.GetConjID().DocID())
			} else { //exclude

				for i := k; i < fieldScanners.Len(); i++ {
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
		//sort.Sort(fieldScanners)
		fieldScanners.Sort()
	}
	return result
}

func (bi *SizeGroupedBEIndex) Retrieve(queries Assignments) (result DocIDList, err error) {

	ctx := &RetrieveContext{
		assigns: queries,
		option:  defaultQueryOption,
	}

	for k := util.MinInt(queries.Size(), bi.maxK()); k >= 0; k-- {

		fieldScanners, err := bi.initPlEntriesScanners(ctx, k)
		if err != nil {
			return nil, err
		}

		tempK := k
		if tempK == 0 {
			tempK = 1
		}
		if len(fieldScanners) < tempK {
			continue
		}
		res := bi.retrieveK(fieldScanners, tempK)
		result = append(result, res...)
	}
	return result, nil
}

func (bi *SizeGroupedBEIndex) DumpEntries() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(wildcardQKey.String())
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")

	for size, container := range bi.kSizeContainers {
		sb.WriteString(fmt.Sprintf("K:%d >>>>>>\n", size))
		container.DumpString(&sb)
	}
	return sb.String()
}

func (bi *SizeGroupedBEIndex) DumpEntriesSummary() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("wildcard entries length:%d >>>>>>\n", len(bi.wildcardEntries)))
	for k := range bi.kSizeContainers {
		sb.WriteString(fmt.Sprintf("SizeEntries k:%d  >>>>>>\n", k))
	}
	return sb.String()
}
