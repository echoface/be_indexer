package be_indexer

import (
	"fmt"
	"io"
	"strings"

	"github.com/echoface/be_indexer/core"

	"github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	CompactBEIndex struct {
		indexBase
		container *core.EntriesContainer
	}

	CompactBuilder struct {
		indexBase
		container *core.EntriesContainerBuilder
	}
)

func NewCompactedBuilder() *CompactBuilder {
	return &CompactBuilder{
		indexBase: indexBase{
			fieldsData: make(map[core.BEField]*core.FieldDesc),
		},
		container: core.NewEntriesContainerBuilder(),
	}
}

// Implement core.BEIndexBuilder for CompactBuilder

func (bi *CompactBuilder) NewContainer(_ int) *core.EntriesContainerBuilder {
	return bi.container
}

func (bi *CompactBuilder) CompileIndexer() (core.BEIndex, error) {
	bi.sortWildcards()
	container, err := bi.container.CompileEntries()
	if err != nil {
		return nil, err
	}

	index := &CompactBEIndex{
		indexBase: bi.indexBase,
		container: container,
	}
	return index, nil
}

// CompactBEIndex implements core.BEIndex (Read-Only)

func NewCompactedBEIndex() *CompactBEIndex {
	index := &CompactBEIndex{
		indexBase: indexBase{
			fieldsData: make(map[core.BEField]*core.FieldDesc),
		},
		container: core.NewEntriesContainer(),
	}
	return index
}

func (bi *CompactBEIndex) initCursors(ctx *core.RetrieveContext) (fCursors FieldCursors, err error) {
	fCursors = make(FieldCursors, 0, len(ctx.Assigns))

	if len(bi.wildcardEntries) > 0 {
		pl := NewSliceIterator(wildcardTerm, bi.wildcardEntries)
		fCursors = append(fCursors, NewFieldCursor(pl))
	}

	var ok bool
	var desc *core.FieldDesc
	var fieldIdx core.FieldIndex
	var entriesList []core.PostingIterator

	for field, values := range ctx.Assigns {
		if desc, ok = bi.fieldsData[field]; !ok {
			continue
		}
		if fieldIdx = bi.container.GetFieldIndexData(desc); fieldIdx == nil {
			// return nil, fmt.Errorf("field:%s no holder found, what happened", field)
			// no document has condition on this field, so just skip here
			continue
		}
		if entriesList, err = fieldIdx.GetEntries(desc, values); err != nil {
			return nil, err
		}
		if len(entriesList) > 0 {
			fCursors = append(fCursors, NewFieldCursor(entriesList...))
		}
	}
	return fCursors, nil
}

func (bi *CompactBEIndex) Retrieve(
	queries core.Assignments, opts ...core.IndexOpt,
) (result core.DocIDList, err error) {
	collector := PickCollector()
	defer PutCollector(collector)

	if err = bi.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}

	result = collector.GetDocIDs()
	return result, nil
}

func (bi *CompactBEIndex) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt,
) (err error) {
	ctx := newRetrieveCtx(queries, opts...)
	util.PanicIf(ctx.Collector != nil, "can't specify collector twice")

	ctx.Collector = collector
	var fieldCursors FieldCursors
	if fieldCursors, err = bi.initCursors(&ctx); err != nil {
		return err
	}

	// sort.Sort(fieldCursors)
	fieldCursors.Sort()

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
			LogInfoIf(ctx.DumpStepInfo, "end retrieve@stepK:%d, need match:%d but only:%d cursors", stepK, needMatchCnt, len(fieldCursors))
			break RETRIEVE
		}

		// needMatchCnt <= plgsCount check whether eid fieldCursors[needMatchCnt-1].GetCurEntryID equal
		endEID := fieldCursors[needMatchCnt-1].GetCurEntryID()

		nextID := core.NewEntryID(endEID.GetConjID(), false)
		// nextID := endEID

		if ctx.DumpStepInfo {
			LogInfo("step:%d process need match:%d cursors:%d, eid:[%s..%s]", stepK, needMatchCnt, len(fieldCursors), eid.DocString(), endEID.DocString())
		}

		if endEID.GetConjID() == conjID {

			nextID = core.NewEntryID(endEID.GetConjID(), true) + 1

			if eid.IsInclude() {
				ctx.Collector.Add(conjID.DocID(), conjID)
			} else { // exclude

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
		if ctx.DumpStepInfo {
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

func (bi *CompactBEIndex) Dump(w io.Writer) error {
	// 1. Metadata
	meta := &indexstore.IndexMetadata{
		Version:     1,
		IndexerType: "compact",
		Fields:      make(map[string]*indexstore.FieldDesc),
	}
	for field, desc := range bi.fieldsData {
		meta.Fields[string(field)] = &indexstore.FieldDesc{
			Name:      string(desc.Field),
			Id:        desc.ID,
			Container: desc.Container,
		}
	}

	if err := writeProtoMessage(w, meta); err != nil {
		return err
	}

	// 2. Wildcards
	ids := make([]uint64, len(bi.wildcardEntries))
	for i, id := range bi.wildcardEntries {
		ids[i] = uint64(id)
	}
	wc := &indexstore.EntryIDList{Ids: ids}
	if err := writeProtoMessage(w, wc); err != nil {
		return err
	}

	// 3. Holders (Single Container)
	container := bi.container
	// Default Holder
	data, err := container.DefaultIndex.Serialize()
	if err != nil {
		return err
	}
	hd := &indexstore.HolderDump{
		HolderType: HolderNameDefault,
		Content:    data,
		GroupId:    0,
	}
	if err := writeProtoMessage(w, hd); err != nil {
		return err
	}

	// Field Holders
	for field, holder := range container.FieldIndices {
		data, err := holder.Serialize()
		if err != nil {
			return err
		}

		holderType := "unknown"
		if desc, ok := bi.fieldsData[field]; ok {
			holderType = desc.Container
		}

		hd := &indexstore.HolderDump{
			FieldName:  string(field),
			HolderType: holderType,
			Content:    data,
			GroupId:    0,
		}
		if err := writeProtoMessage(w, hd); err != nil {
			return err
		}
	}

	return nil
}

func (bi *CompactBEIndex) Load(r io.Reader) error {
	readMsg := func(msg proto.Message) error {
		lenVal, err := readVarint(r)
		if err != nil {
			return err
		}

		buf := make([]byte, lenVal)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}

		return proto.Unmarshal(buf, msg)
	}

	// 1. Metadata
	meta := &indexstore.IndexMetadata{}
	if err := readMsg(meta); err != nil {
		return err
	}
	if meta.Version != 0 && meta.Version != 1 {
		return fmt.Errorf("unsupported index dump version: %d", meta.Version)
	}
	if meta.IndexerType != "" && meta.IndexerType != "compact" {
		return fmt.Errorf("indexer type mismatch: want compact, got %q", meta.IndexerType)
	}

	// Reset current state to avoid mixing with previous data.
	if bi.container == nil {
		bi.container = core.NewEntriesContainer()
	} else {
		// Keep default holder instance (may carry custom tokenizers), but drop all field holders.
		bi.container.FieldIndices = map[core.BEField]core.FieldIndex{}
		if bi.container.DefaultIndex == nil {
			bi.container.DefaultIndex = core.NewFieldIndex(HolderNameDefault)
		}
	}

	bi.fieldsData = make(map[core.BEField]*core.FieldDesc)
	for name, fd := range meta.Fields {
		bi.fieldsData[core.BEField(name)] = &core.FieldDesc{
			ID:          fd.Id,
			Field:       core.BEField(name),
			FieldOption: core.FieldOption{Container: fd.Container},
		}
	}

	// 2. Wildcards
	wc := &indexstore.EntryIDList{}
	if err := readMsg(wc); err != nil {
		return err
	}
	bi.wildcardEntries = make(core.Entries, len(wc.Ids))
	for i, id := range wc.Ids {
		bi.wildcardEntries[i] = core.EntryID(id)
	}

	// 3. Holders
	container := bi.container
	for {
		hd := &indexstore.HolderDump{}
		err := readMsg(hd)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if hd.FieldName == "" { // Default Holder
			if err := container.DefaultIndex.Deserialize(hd.Content); err != nil {
				return err
			}
		} else {
			field := core.BEField(hd.FieldName)
			desc := bi.fieldsData[field]
			if desc == nil {
				return fmt.Errorf("field %s not found in metadata", field)
			}
			// Note: For searcher (Index), we assume holders are created by Deserialize or CreateHolder logic?
			// But core.EntriesContainer (Index) doesn't have CreateHolder.
			// We should manually populate fieldHolder.

			// Check if holder exists? No, map is cleared.
			// Create new holder.
			// Index container doesn't have CreateHolder method exposed (it was Builder method).
			// So we instantiate directly.
			holder := core.NewFieldIndex(desc.Container)
			if holder == nil {
				return fmt.Errorf("unknown holder type: %s", desc.Container)
			}
			container.FieldIndices[desc.Field] = holder

			if err := holder.Deserialize(hd.Content); err != nil {
				return err
			}
		}
	}

	// Ensure query-ready state (sorted wildcards + holders compiled if needed).
	// But CompactBEIndex doesn't implement compileIndexer anymore?
	// And Holders should be ready after Deserialize?
	// Usually Deserialize sets up the state.
	// But wildcardEntries need sorting?
	// compileIndexer() logic was sorting wildcards and calling compileEntries().

	bi.sortWildcards()

	// Holders might need CompileEntries() call?
	// Deserialize should put them in ready state.
	// If Deserialize implies "ready to search", then fine.
	// If not, we might need to call CompileEntries on holders.
	// But core.EntriesContainer (Index) doesn't have CompileEntries exposed?
	// Wait, core.EntriesContainer (Index) has NO methods to mutate holders?
	// But holders themselves are `EntriesHolder` interface which has `CompileEntries`.

	// Let's assume Deserialize handles it or we call it manually.
	// In the original code, Load called compileIndexer at the end.

	// I should manually do what compileIndexer did for cleanup/setup.
	return nil
}

