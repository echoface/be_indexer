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

func (bi *CompactedBEIndex) initFieldScanner(ctx *RetrieveContext) (fScanners FieldScanners, err error) {

	fScanners = make(FieldScanners, 0, len(ctx.assigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewEntriesCursor(wildcardQKey, bi.wildcardEntries)
		fScanners = append(fScanners, NewFieldScanner(pl))
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
			fScanners = append(fScanners, NewFieldScanner(entriesList...))
		}
	}

	if len(fScanners) == 0 {
		return fScanners, nil
	}

	if ctx.DumpEntriesDetail {
		Logger.Infof("matched entries\n%s", fScanners.Dump())
	}
	return fScanners, nil
}

func (bi *CompactedBEIndex) Retrieve(queries Assignments, opts ...IndexOpt) (result DocIDList, err error) {

	ctx := &RetrieveContext{
		assigns: queries,
	}
	for _, fn := range opts {
		fn(ctx)
	}
	if !ctx.userCollector {
		ctx.collector = collectorPool.Get().(*DocIDCollector)
	}

	var fieldScanners FieldScanners
	fieldScanners, err = bi.initFieldScanner(ctx)
	if err != nil {
		return nil, err
	}

	result = make([]DocID, 0, 128)

	fieldScanners.Sort()

RETRIEVE:
	for {
		if len(fieldScanners) == 0 {
			break RETRIEVE
		}

		eid := fieldScanners[0].GetCurEntryID()

		// k means: need k numbers of eid has same values when document satisfied,
		// but for Z entries, it's a special case that need logic k=1 to exclude docs
		// that boolean expression has `exclude` logic
		k := eid.GetConjID().Size()
		if k == 0 {
			k = 1
		}

		// remove those entries that have already reached end;
		// the end-up cursor will in the end of slice after sorting
		for len(fieldScanners) > 0 && fieldScanners[len(fieldScanners)-1].GetCurEntryID().IsNULLEntry() {
			fieldScanners = fieldScanners[:len(fieldScanners)-1]
		}

		// k means: need k numbers of eid has same values when document satisfied,
		// so we can end up loop safely when k > sizeof(fieldCursors). this will boost up retrieve speed
		if k > len(fieldScanners) {
			if ctx.DumpStepInfo {
				Logger.Infof("end, step result\n%+v @k:%d", result, k)
			}
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
		if ctx.DumpStepInfo {
			Logger.Infof("step result\n%+v", result)
			Logger.Infof("sorted entries\n%s", fieldScanners.Dump())
		}
	}

	if ctx.userCollector {
		return nil, nil
	}
	// default collector
	collector, ok := ctx.collector.(*DocIDCollector)
	util.PanicIf(!ok, "should not reach")

	result = make(DocIDList, 0, collector.DocCount())
	iter := collector.docBits.Iterator()
	if iter.HasNext() {
		result = append(result, DocID(iter.Next()))
	}
	collectorPool.Put(collector)
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
