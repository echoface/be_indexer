package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/util"
)

type (
	CompactBEIndex struct {
		indexBase
		container *EntriesContainer
	}
)

func NewCompactedBEIndex() *CompactBEIndex {
	index := &CompactBEIndex{
		indexBase: indexBase{
			fieldsData: make(map[BEField]*FieldDesc),
		},
		container: newFieldEntriesContainer(),
	}
	return index
}

// newContainer(k int)
func (bi *CompactBEIndex) newContainer(_ int) *EntriesContainer {
	return bi.container
}

func (bi *CompactBEIndex) compileIndexer() error {
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
	return bi.container.compileEntries()
}

func (bi *CompactBEIndex) initCursors(ctx *retrieveContext) (fCursors FieldCursors, err error) {

	fCursors = make(FieldCursors, 0, len(ctx.assigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(wildcardQKey, bi.wildcardEntries)
		fCursors = append(fCursors, NewFieldCursor(pl))
	}

	var ok bool
	var desc *FieldDesc
	var holder EntriesHolder
	var entriesList EntriesCursors

	for field, values := range ctx.assigns {
		if desc, ok = bi.fieldsData[field]; !ok {
			continue
		}
		if holder = bi.container.getFieldHolder(desc); holder == nil {
			// return nil, fmt.Errorf("field:%s no holder found, what happened", field)
			// no document has condition on this field, so just skip here
			continue
		}
		if entriesList, err = holder.GetEntries(desc, values); err != nil {
			return nil, err
		}
		if len(entriesList) > 0 {
			fCursors = append(fCursors, NewFieldCursor(entriesList...))
		}
	}
	return fCursors, nil
}

func (bi *CompactBEIndex) Retrieve(
	queries Assignments, opts ...IndexOpt) (result DocIDList, err error) {

	collector := PickCollector()
	defer PutCollector(collector)

	if err = bi.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}

	result = collector.GetDocIDs()
	return result, nil
}

func (bi *CompactBEIndex) RetrieveWithCollector(
	queries Assignments, collector ResultCollector, opts ...IndexOpt) (err error) {

	ctx := newRetrieveCtx(queries, opts...)
	util.PanicIf(ctx.collector != nil, "can't specify collector twice")

	ctx.collector = collector
	var fieldCursors FieldCursors
	if fieldCursors, err = bi.initCursors(&ctx); err != nil {
		return err
	}

	// sort.Sort(fieldCursors)
	fieldCursors.Sort()
	if ctx.dumpEntriesDetail {
		Logger.Infof("RetrieveWithCollector initial entries:\n%s", fieldCursors.Dump())
	}

RETRIEVE:
	for len(fieldCursors) > 0 {

		eid := fieldCursors[0].GetCurEntryID()
		conjID := eid.GetConjID()

		// needMatchCnt means: need at least needMatchCnt field has same eid when conjunction expr satisfied,
		// so we can end up loop safely when needMatchCnt > sizeof(fieldCursors). this will boost up retrieve speed
		// but for Z entries, it's a special case that need logic needMatchCnt=1 to exclude docs
		// that boolean expression has `exclude` logic
		stepK := conjID.Size()
		needMatchCnt := util.MaxInt(1, stepK)
		if needMatchCnt > len(fieldCursors) {
			LogInfoIf(ctx.dumpStepInfo, "end retrieve@stepK:%d, need match:%d but only:%d cursors", stepK, needMatchCnt, len(fieldCursors))
			break RETRIEVE
		}

		// needMatchCnt <= plgsCount check whether eid fieldCursors[needMatchCnt-1].GetCurEntryID equal
		endEID := fieldCursors[needMatchCnt-1].GetCurEntryID()

		nextID := NewEntryID(endEID.GetConjID(), false)
		// nextID := endEID

		if ctx.dumpEntriesDetail {
			Logger.Infof("step:%d round start, docs:%v entries:\n%s", stepK, collector.GetDocIDs(), fieldCursors.Dump())
		}
		if ctx.dumpStepInfo {
			LogInfo("step:%d process need match:%d cursors:%d, eid:[%s..%s]", stepK, needMatchCnt, len(fieldCursors), eid.DocString(), endEID.DocString())
		}

		if endEID.GetConjID() == conjID {

			nextID = NewEntryID(endEID.GetConjID(), true) + 1

			if eid.IsInclude() {

				ctx.collector.Add(conjID.DocID(), conjID)

			} else { //exclude

				for i := needMatchCnt; i < len(fieldCursors); i++ {
					if fieldCursors[i].GetCurEntryID() < nextID {
						fieldCursors[i].SkipTo(nextID)
					}
				}
			}
		}

		for i := 0; i < needMatchCnt; i++ { // 推进游标
			fieldCursors[i].SkipTo(nextID)
		}

		fieldCursors.Sort()
		// sort.Sort(fieldCursors) // slow 12% compare to fieldCursors.Sort()

		// remove those entries that have already reached end;
		// the end-up cursor will in the end of slice after sorting
		for len(fieldCursors) > 0 && fieldCursors[len(fieldCursors)-1].ReachEnd() {
			fieldCursors = fieldCursors[:len(fieldCursors)-1]
		}
		if ctx.dumpStepInfo {
			Logger.Infof("step:%d round end, result docs:%+v", stepK, collector.GetDocIDs())
		}
	}

	return nil
}

// DumpIndexInfo summary info about this indexer
// +++++++ compact boolean indexing info +++++++++++
// wildcard info: count: N
// default holder: {name:%s value_count:%d, max_entries:%d avg_entries:%d}
// field holder:
//
//	>field:%s {name: %s, value_count:%d max_entries:%d avg_entries:%d}
//	>field:%s {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (bi *CompactBEIndex) DumpIndexInfo(sb *strings.Builder) {
	sb.WriteString("\n+++++++ compact boolean indexing info +++++++++++\n")
	sb.WriteString(fmt.Sprintf("wildcard info: count:%d\n", len(bi.wildcardEntries)))
	bi.container.DumpInfo(sb)
	sb.WriteString("\n++++++++++++++dump index info end ++++++++++++++++\n")
}

func (bi *CompactBEIndex) DumpEntries(sb *strings.Builder) {
	sb.WriteString("\n+++++++ compact boolean indexing entries +++++++++++\n")
	sb.WriteString(fmt.Sprintf("Z:\n"))
	sb.WriteString(wildcardQKey.String())
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")
	bi.container.DumpEntries(sb)
	sb.WriteString("\n+++++++++++++ dump entries end ++++++++++++++++++++++\n")
}
