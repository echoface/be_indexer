package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/util"
)

type (
	// for default Entries Holder, it can hold different field's entries,
	// but for ACMatcher or other Holder, it may only hold entries for one field
	fieldEntriesContainer struct {
		defaultHolder EntriesHolder // shared entries holder

		fieldHolder map[BEField]EntriesHolder // special field holder
	}

	SizeGroupedBEIndex struct {
		indexBase
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
	c.defaultHolder.CompileEntries()

	for _, holder := range c.fieldHolder {

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

	c.fieldHolder[desc.Field] = holder

	return holder
}

func (c *fieldEntriesContainer) DumpString(buf *strings.Builder) {
	c.defaultHolder.DumpEntries(buf)
	for field, holder := range c.fieldHolder {
		buf.WriteString(fmt.Sprintf("field:%s entries:", field))
		holder.DumpEntries(buf)
	}
}

func NewSizeGroupedBEIndex() BEIndex {
	index := &SizeGroupedBEIndex{
		indexBase: indexBase{
			fieldsData: make(map[BEField]*FieldDesc),
		},
		kSizeContainers: make([]*fieldEntriesContainer, 0),
	}
	return index
}

func (bi *SizeGroupedBEIndex) maxK() int {
	return len(bi.kSizeContainers) - 1
}

// newEntriesContainerIfNeeded(k int) *fieldEntriesContainer get a container if created, otherwise create one
func (bi *SizeGroupedBEIndex) newContainer(k int) *fieldEntriesContainer {
	for k >= len(bi.kSizeContainers) {
		container := newFieldEntriesContainer()
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

func (bi *SizeGroupedBEIndex) initFieldCursors(ctx *retrieveContext, k int) (fCursors FieldCursors, err error) {

	fCursors = make(FieldCursors, 0, len(bi.fieldsData))

	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(wildcardQKey, bi.wildcardEntries)
		fCursors = append(fCursors, NewFieldCursor(pl))
	}

	kSizeContainer := bi.getKSizeEntries(k)

	var entriesList CursorGroup
	var holder EntriesHolder

	var ok bool
	var desc *FieldDesc

	for field, values := range ctx.assigns {

		if desc, ok = bi.fieldsData[field]; !ok {
			// not recognized field, no document care about this field, ignore
			continue
		}

		if holder = kSizeContainer.getFieldHolder(desc); holder == nil {
			// Logger.Debugf("entries holder not found, field:%s", desc.Field)
			// case 1: user/client can pass non-exist assign, so just skip this field
			// case 2: no any document has condition on this field
			continue
		}

		if entriesList, err = holder.GetEntries(desc, values); err != nil {
			Logger.Errorf("fetch entries from holder fail:%s, field:%s\n", err.Error(), desc.Field)
			return nil, err
		}

		if len(entriesList) > 0 {
			scanner := NewFieldCursor(entriesList...)
			fCursors = append(fCursors, scanner)

			Logger.Debugf("<%s,%v> fetch %d posting list\n", desc.Field, values, len(entriesList))
		} else {
			Logger.Debugf("<%s,%v> nothing matched from entries holder\n", desc.Field, values)
		}
	}

	if ctx.dumpEntriesDetail {
		Logger.Infof("matched entries:\n%s", fCursors.Dump())
	}

	return fCursors, nil
}

// retrieveK MOVE TO: FieldCursors ?
func (bi *SizeGroupedBEIndex) retrieveK(ctx *retrieveContext, fieldCursors FieldCursors, k int) {

	// sort.Sort(fieldCursors)
	fieldCursors.Sort()

	for !fieldCursors[k-1].GetCurEntryID().IsNULLEntry() {

		eid := fieldCursors[0].GetCurEntryID()
		endEID := fieldCursors[k-1].GetCurEntryID()

		conjID := eid.GetConjID()
		endConjID := endEID.GetConjID()

		nextID := NewEntryID(endConjID, false)

		if conjID == endConjID {

			nextID = endEID + 1

			if eid.IsInclude() {

				ctx.collector.Add(conjID.DocID(), conjID)
				if ctx.dumpStepInfo {
					Logger.Infof("step k:%d add doc:%d conj:%d\n", k, conjID.DocID(), conjID)
				}
			} else { //exclude

				for i := k; i < fieldCursors.Len(); i++ {
					if fieldCursors[i].GetCurConjID() != eid.GetConjID() {
						break
					}
					fieldCursors[i].Skip(nextID)
				}
			}
		}
		// 推进游标
		for i := 0; i < k; i++ {
			fieldCursors[i].SkipTo(nextID)
		}

		// sort.Sort(fieldCursors)
		fieldCursors.Sort()

		if ctx.dumpStepInfo {
			Logger.Infof("sorted entries\n%s", fieldCursors.Dump())
		}
	}
}

func (bi *SizeGroupedBEIndex) Retrieve(
	queries Assignments, opts ...IndexOpt) (result DocIDList, err error) {

	collector := PickCollector()

	defer PutCollector(collector)

	if err = bi.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}

	result = collector.GetDocIDs()

	return result, nil
}

func (bi *SizeGroupedBEIndex) RetrieveWithCollector(
	queries Assignments, collector ResultCollector, opts ...IndexOpt) (err error) {

	ctx := newRetrieveCtx(queries, opts...)
	util.PanicIf(ctx.collector != nil, "can't specify collector twice")

	ctx.collector = collector

	var fCursors FieldCursors
	for k := util.MinInt(queries.Size(), bi.maxK()); k >= 0; k-- {
		if ctx.dumpStepInfo {
			Logger.Infof("start retrieve k:", k)
		}
		if fCursors, err = bi.initFieldCursors(&ctx, k); err != nil {
			return err
		}

		tempK := k
		if tempK == 0 {
			tempK = 1
		}

		if len(fCursors) < tempK {
			continue
		}

		bi.retrieveK(&ctx, fCursors, tempK)
	}
	return nil
}

func (bi *SizeGroupedBEIndex) DumpEntries() string {
	sb := strings.Builder{}
	sb.WriteString(fmt.Sprintf("Z:>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n"))
	sb.WriteString(wildcardQKey.String())
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")

	for size, container := range bi.kSizeContainers {
		sb.WriteString(fmt.Sprintf("\nK:%d >>>>>>>>>>>>>>>>>>>>>>>>>>>>>>\n", size))
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
