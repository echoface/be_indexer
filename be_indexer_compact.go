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

// newPostingEntriesIfNeeded(k int)
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

	if len(fCursors) == 0 {
		return fCursors, nil
	}

	if ctx.dumpEntriesDetail {
		Logger.Infof("matched entries:\n%s", fCursors.Dump())
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
	fieldCursors, err = bi.initCursors(&ctx)
	if err != nil {
		return err
	}

	fieldCursors.Sort()
	// sort.Sort(fieldCursors)

RETRIEVE:
	for {
		if len(fieldCursors) == 0 {
			break RETRIEVE
		}

		eid := fieldCursors[0].GetCurEntryID()
		conjID := eid.GetConjID()

		// k means: need k numbers of eid has same values when document satisfied,
		// but for Z entries, it's a special case that need logic k=1 to exclude docs
		// that boolean expression has `exclude` logic
		k := conjID.Size()
		if k == 0 {
			k = 1
		}

		// remove those entries that have already reached end;
		// the end-up cursor will in the end of slice after sorting
		for len(fieldCursors) > 0 && fieldCursors[len(fieldCursors)-1].ReachEnd() {
			fieldCursors = fieldCursors[:len(fieldCursors)-1]
		}

		// k means: need k numbers of eid has same values when document satisfied,
		// so we can end up loop safely when k > sizeof(fieldCursors). this will boost up retrieve speed
		if k > len(fieldCursors) {
			if ctx.dumpStepInfo {
				Logger.Infof("end, step k:%d, k > fieldCursors.len", k)
			}
			break RETRIEVE
		}

		// k <= plgsCount
		// check whether eid  fieldCursors[k-1].GetCurEntryID equal
		endEID := fieldCursors[k-1].GetCurEntryID()

		nextID := NewEntryID(endEID.GetConjID(), false)
		if endEID.GetConjID() == conjID {

			nextID = endEID + 1

			if eid.IsInclude() {

				ctx.collector.Add(conjID.DocID(), conjID)

				if ctx.dumpStepInfo {
					Logger.Infof("step k:%d add doc:%d conj:%d\n", k, conjID.DocID(), conjID)
				}
			} else { //exclude

				for i := k; i < len(fieldCursors); i++ {
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

		fieldCursors.Sort()
		// sort.Sort(fieldCursors) // slow 12% compare to fieldCursors.Sort()

		if ctx.dumpStepInfo {
			Logger.Infof("step result\n%+v", collector.GetDocIDs())
			Logger.Infof("sorted entries\n%s", fieldCursors.Dump())
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
	sb.WriteString("\n++++++++++++++++++ end ++++++++++++++++++++++++\n")
}

func (bi *CompactBEIndex) DumpEntries(sb *strings.Builder) {
	sb.WriteString("\n+++++++ compact boolean indexing entries +++++++++++\n")
	sb.WriteString(fmt.Sprintf("Z:\n"))
	sb.WriteString(wildcardQKey.String())
	sb.WriteString(":")
	sb.WriteString(fmt.Sprintf("%v", bi.wildcardEntries.DocString()))
	sb.WriteString("\n")
	bi.container.DumpEntries(sb)
	sb.WriteString("\n++++++++++++++++++ end ++++++++++++++++++++++++\n")
}
