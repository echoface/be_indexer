package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/util"
)

type (
	CompactedBEIndex struct {
		indexBase
		fieldContainer *fieldEntriesContainer
	}
)

func NewCompactedBEIndex() *CompactedBEIndex {
	index := &CompactedBEIndex{
		indexBase: indexBase{
			fieldsData: make(map[BEField]*FieldDesc),
		},
		fieldContainer: newFieldEntriesContainer(),
	}
	return index
}

// newPostingEntriesIfNeeded(k int)
func (bi *CompactedBEIndex) newContainer(_ int) *fieldEntriesContainer {
	return bi.fieldContainer
}

func (bi *CompactedBEIndex) compileIndexer() {
	if bi.wildcardEntries.Len() > 0 {
		sort.Sort(bi.wildcardEntries)
	}
	bi.fieldContainer.compileEntries()
}

func (bi *CompactedBEIndex) initCursors(ctx *retrieveContext) (fCursors FieldCursors, err error) {

	fCursors = make(FieldCursors, 0, len(ctx.assigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(wildcardQKey, bi.wildcardEntries)
		fCursors = append(fCursors, NewFieldCursor(pl))
	}

	var ok bool
	var desc *FieldDesc
	var holder EntriesHolder
	var entriesList CursorGroup

	for field, values := range ctx.assigns {
		if desc, ok = bi.fieldsData[field]; !ok {
			continue
		}
		if holder = bi.fieldContainer.getFieldHolder(desc); holder == nil {
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

func (bi *CompactedBEIndex) Retrieve(
	queries Assignments, opts ...IndexOpt) (result DocIDList, err error) {

	collector := PickCollector()
	defer PutCollector(collector)

	if err = bi.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}

	result = collector.GetDocIDs()
	return result, nil
}

func (bi *CompactedBEIndex) RetrieveWithCollector(
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
		if ctx.dumpStepInfo {
			Logger.Infof("step result\n%+v", collector.GetDocIDs())
			Logger.Infof("sorted entries\n%s", fieldCursors.Dump())
		}
	}

	return nil
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
