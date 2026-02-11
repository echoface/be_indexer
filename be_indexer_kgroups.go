package be_indexer

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	indexstore "github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	KGroupsBEIndex struct {
		indexBase
		kSizeContainers []*core.EntriesContainer
	}

	// KGroupsBuilder implements core.BEIndexBuilder for KGroups strategy
	KGroupsBuilder struct {
		indexBase
		kSizeContainers []*core.EntriesContainerBuilder
	}
)

// NewKGroupsBuilder creates a new builder for KGroups strategy
func NewKGroupsBuilder() *KGroupsBuilder {
	return &KGroupsBuilder{
		indexBase: indexBase{
			fieldsData: make(map[core.BEField]*core.FieldDesc),
		},
		kSizeContainers: make([]*core.EntriesContainerBuilder, 0),
	}
}

// Implement core.BEIndexBuilder for KGroupsBuilder

func (bi *KGroupsBuilder) NewContainer(k int) *core.EntriesContainerBuilder {
	for k >= len(bi.kSizeContainers) {
		container := core.NewEntriesContainerBuilder()
		bi.kSizeContainers = append(bi.kSizeContainers, container)
	}
	return bi.kSizeContainers[k]
}

func (bi *KGroupsBuilder) CompileIndexer() (core.BEIndex, error) {
	containers := make([]*core.EntriesContainer, len(bi.kSizeContainers))
	for i, sizeEntries := range bi.kSizeContainers {
		container, err := sizeEntries.CompileEntries()
		if err != nil {
			return nil, err
		}
		containers[i] = container
	}
	bi.sortWildcards()

	index := &KGroupsBEIndex{
		indexBase:       bi.indexBase, // shallow copy fieldsData and wildcardEntries
		kSizeContainers: containers,
	}
	return index, nil
}

// KGroupsBEIndex implements core.BEIndex (Read-Only Searcher part)

func NewKGroupsBEIndex() core.BEIndex {
	// This constructor is now deprecated for direct building use,
	// but might be used for empty init or loading.
	// We will keep it but it acts as an empty index.
	return &KGroupsBEIndex{
		indexBase: indexBase{
			fieldsData: make(map[core.BEField]*core.FieldDesc),
		},
		kSizeContainers: make([]*core.EntriesContainer, 0),
	}
}

func (bi *KGroupsBEIndex) maxK() int {
	return len(bi.kSizeContainers) - 1
}

func (bi *KGroupsBEIndex) ensureContainer(k int) *core.EntriesContainer {
	for len(bi.kSizeContainers) <= k {
		bi.kSizeContainers = append(bi.kSizeContainers, core.NewEntriesContainer())
	}
	return bi.kSizeContainers[k]
}

func (bi *KGroupsBEIndex) compileIndexer() error {
	// Already compiled by builder
	return nil
}

// ... (Rest of KGroupsBEIndex methods remain)

func (bi *KGroupsBEIndex) getKSizeEntries(k int) *core.EntriesContainer {
	if k >= len(bi.kSizeContainers) {
		panic(fmt.Errorf("k:[%d] out of range", k))
	}
	return bi.kSizeContainers[k]
}

func (bi *KGroupsBEIndex) initCursors(ctx *core.RetrieveContext, k int) (fCursors FieldCursors, err error) {
	fCursors = make(FieldCursors, 0, len(bi.fieldsData))

	if k == 0 && len(bi.wildcardEntries) > 0 {
		pl := NewSliceIterator(wildcardTerm, bi.wildcardEntries)
		fCursors = append(fCursors, NewFieldCursor(pl))
	}

	kSizeContainer := bi.getKSizeEntries(k)

	var fieldIdx core.FieldIndex
	var entriesList []core.PostingIterator

	var ok bool
	var desc *core.FieldDesc

	for field, values := range ctx.Assigns {

		if desc, ok = bi.fieldsData[field]; !ok {
			// not recognized field, no document care about this field, ignore
			continue
		}

		if fieldIdx = kSizeContainer.GetFieldIndexData(desc); fieldIdx == nil {
			// Logger.Debugf("entries holder not found, field:%s", desc.Field)
			// case 1: user/client can pass non-exist assign, so just skip this field
			// case 2: no any document has condition on this field
			continue
		}

		if entriesList, err = fieldIdx.GetEntries(desc, values); err != nil {
			Logger.Errorf("fetch entries from holder fail:%s, field:%s\n", err.Error(), desc.Field)
			return nil, err
		}

		if len(entriesList) > 0 {
			fCursors = append(fCursors, NewFieldCursor(entriesList...))
			Logger.Debugf("<%s,%v> fetch %d posting list\n", desc.Field, values, len(entriesList))
		} else {
			Logger.Debugf("<%s,%v> nothing matched from entries holder\n", desc.Field, values)
		}
	}
	return fCursors, nil
}

// retrieveK retrieve matched result from k size index data
func (bi *KGroupsBEIndex) retrieveK(ctx *core.RetrieveContext, fieldCursors FieldCursors, needMatchCnt int) {
	if len(fieldCursors) < needMatchCnt {
		LogInfoIf(ctx.DumpStepInfo, "need match:%d but only:%d", needMatchCnt, len(fieldCursors))
		return
	}
	// sort.Sort(fieldCursors)
	fieldCursors.Sort()

	for !fieldCursors[needMatchCnt-1].GetCurEntryID().IsNULLEntry() {
		if ctx.DumpStepInfo {
			Logger.Infof("round need match:%d continue docs:%v", needMatchCnt, ctx.Collector.GetDocIDs())
		}

		eid := fieldCursors[0].GetCurEntryID()
		endEID := fieldCursors[needMatchCnt-1].GetCurEntryID()

		conjID := eid.GetConjID()
		endConjID := endEID.GetConjID()

		nextID := core.NewEntryID(endConjID, false) // 逻辑按照conjID执行，但是直接使用endEID可能跳过排除逻辑的EID

		if conjID == endConjID {

			// nextID = endEID + 1
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
		// sort.Sort(fieldCursors)
		if ctx.DumpStepInfo {
			Logger.Infof("round end need match:%d, docs:%v", needMatchCnt, ctx.Collector.GetDocIDs())
		}
	}
}

func (bi *KGroupsBEIndex) Retrieve(
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

func (bi *KGroupsBEIndex) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt,
) (err error) {
	ctx := newRetrieveCtx(queries, opts...)
	util.PanicIf(ctx.Collector != nil, "can't specify collector twice")

	ctx.Collector = collector

	var fCursors FieldCursors
	for k := util.MinInt(queries.Size(), bi.maxK()); k >= 0; k-- {
		if fCursors, err = bi.initCursors(&ctx, k); err != nil {
			return err
		}
		LogInfoIf(ctx.DumpStepInfo, "start@step:%d cursors:%d", k, len(fCursors))

		needMatchCnt := util.MaxInt(k, 1)
		bi.retrieveK(&ctx, fCursors, needMatchCnt)
	}
	return nil
}

func (bi *KGroupsBEIndex) DumpIndexInfo(sb *strings.Builder) {
	sb.WriteString("\n+++++++ size grouped boolean indexing info +++++++++++\n")
	sb.WriteString(fmt.Sprintf("wildcard info: count:%d\n", len(bi.wildcardEntries)))
	for k, c := range bi.kSizeContainers {
		sb.WriteString(fmt.Sprintf(">> container for size k:%d\n", k))
		c.DumpInfo(sb)
		sb.WriteString("\n")
	}
	sb.WriteString("++++++++++++ size grouped index info end ++++++++++++++++++\n")
}

func (bi *KGroupsBEIndex) Dump(w io.Writer) error {
	// 1. Metadata
	meta := &indexstore.IndexMetadata{
		Version:     1,
		IndexerType: "kgroups",
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

	// 3. Holders
	for k, container := range bi.kSizeContainers {
		// Default Holder
		data, err := container.DefaultIndex.Serialize()
		if err != nil {
			return err
		}
		hd := &indexstore.HolderDump{
			HolderType: HolderNameDefault,
			Content:    data,
			GroupId:    int32(k),
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
				GroupId:    int32(k),
			}
			if err := writeProtoMessage(w, hd); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeProtoMessage(w io.Writer, msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	lenBuf := make([]byte, 8)
	n := binary.PutUvarint(lenBuf, uint64(len(data)))
	if _, err := w.Write(lenBuf[:n]); err != nil {
		return err
	}

	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}

func (bi *KGroupsBEIndex) Load(r io.Reader) error {
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
	if meta.IndexerType != "" && meta.IndexerType != "kgroups" {
		return fmt.Errorf("indexer type mismatch: want kgroups, got %q", meta.IndexerType)
	}

	// Reset current state to avoid mixing with previous data.
	bi.kSizeContainers = bi.kSizeContainers[:0]

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
	for {
		hd := &indexstore.HolderDump{}
		err := readMsg(hd)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		container := bi.ensureContainer(int(hd.GroupId))

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
	return bi.compileIndexer()
}

func readVarint(r io.Reader) (uint64, error) {
	var x uint64
	var s uint
	for i := 0; ; i++ {
		var b [1]byte
		_, err := r.Read(b[:])
		if err != nil {
			return x, err
		}
		if b[0] < 0x80 {
			if i > 9 || i == 9 && b[0] > 1 {
				return x, fmt.Errorf("varint overflow")
			}
			return x | uint64(b[0])<<s, nil
		}
		x |= uint64(b[0]&0x7f) << s
		s += 7
	}
}
