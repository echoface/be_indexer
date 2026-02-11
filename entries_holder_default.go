package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"bytes"
	"fmt"
	"strings"

	"github.com/echoface/be_indexer/codegen/cache"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	KVTerm struct {
		FieldID uint64
		Value   string
	}

	// CompressedKVBuilder: 基于 Segment 架构的 Holder 实现 (构建器)
	CompressedKVBuilder struct {
		debug       bool
		fieldParser map[core.BEField]parser.ValueTokenizer

		builder *MemSegmentBuilder
	}

// CompressedKVIndex: 基于 Segment 架构的 Holder 实现 (只读)
	CompressedKVIndex struct {
		debug       bool
		fieldParser map[core.BEField]parser.ValueTokenizer

		reader *BlockSegmentReader

		// 缓存序列化后的数据，用于 Serve 和 Serialize
		segmentData []byte
	}

	StrTokenData struct {
		cache.StrListValues
	}
)

func init() {
	RegisterField(HolderNameDefault, core.FieldImplementation{
		NewBuilder: func() core.FieldIndexBuilder { return NewCompressedKVBuilder() },
		NewIndex:   func() core.FieldIndex { return NewCompressedKVIndex() },
	})
}

func NewKVTerm(fid uint64, value string) KVTerm {
	return KVTerm{FieldID: fid, Value: value}
}

func (tm KVTerm) String() string {
	return fmt.Sprintf("<%d,%s>", tm.FieldID, tm.Value)
}

// DefaultEntriesHolder 为历史兼容：默认 holder 实际为压缩实现。
type DefaultEntriesHolder = CompressedKVIndex

func NewDefaultEntriesHolder() *CompressedKVBuilder {
	return NewCompressedKVBuilder()
}

func NewCompressedKVBuilder() *CompressedKVBuilder {
	return &CompressedKVBuilder{
		fieldParser: make(map[core.BEField]parser.ValueTokenizer),
		builder:     NewMemSegmentBuilder(),
	}
}

func NewCompressedKVIndex() *CompressedKVIndex {
	return &CompressedKVIndex{
		fieldParser: make(map[core.BEField]parser.ValueTokenizer),
	}
}

func (std *StrTokenData) Encode() ([]byte, error) {
	return proto.Marshal(&std.StrListValues)
}

// DecodeFieldIndexingData decode data; used for building progress cache
func (h *CompressedKVBuilder) DecodeFieldIndexingData(data []byte) (core.IndexingData, error) {
	if len(data) == 0 {
		return &StrTokenData{}, nil
	}
	txData := &StrTokenData{}
	err := proto.Unmarshal(data, &txData.StrListValues)
	return txData, err
}

func (h *CompressedKVBuilder) GetTokenizer(field core.BEField) parser.ValueTokenizer {
	if p, ok := h.fieldParser[field]; ok {
		return p
	}
	return parser.NewDefaultTokenizer()
}

// RegisterFieldTokenizer registers a custom tokenizer for a specific field.
func (h *CompressedKVBuilder) RegisterFieldTokenizer(field core.BEField, tokenizer parser.ValueTokenizer) {
	if tokenizer == nil {
		delete(h.fieldParser, field)
		return
	}
	h.fieldParser[field] = tokenizer
}

func (h *CompressedKVBuilder) CompileEntries() (core.FieldIndex, error) {
	if h.builder == nil {
		return nil, fmt.Errorf("builder is nil")
	}

	// Flush builder to bytes
	var buf bytes.Buffer
	if err := h.builder.Flush(&buf); err != nil {
		return nil, err
	}

	segmentData := buf.Bytes()

	// Open reader
	reader, err := NewBlockSegmentReader(segmentData)
	if err != nil {
		return nil, err
	}

	// Create Holder
	holder := &CompressedKVIndex{
		debug:       h.debug,
		fieldParser: make(map[core.BEField]parser.ValueTokenizer, len(h.fieldParser)),
		reader:      reader,
		segmentData: segmentData,
	}

	// Copy parsers
	for k, v := range h.fieldParser {
		holder.fieldParser[k] = v
	}

	// Free builder memory
	h.builder.Reset()
	h.builder = nil

	return holder, nil
}

// LoadData 仅用于测试/内部：从扁平 entries 重建。
// Note: This modifies the builder and returns a compiled holder.
func (h *CompressedKVBuilder) LoadData(terms []util.KeyEntry[KVTerm], entries []core.EntryID) (core.FieldIndex, error) {
	h.builder = NewMemSegmentBuilder()

	for _, te := range terms {
		start := te.HeaderIndex
		end := te.HeaderIndex + te.Length
		if start < 0 || end > uint32(len(entries)) {
			continue
		}
		for _, eid := range entries[start:end] {
			h.builder.AddEntryWithFieldID(te.Key.FieldID, te.Key.Value, eid)
		}
	}
	return h.CompileEntries()
}

func (h *CompressedKVBuilder) BuildFieldIndexingData(field *core.FieldDesc, bv *core.ValueExpr) (core.IndexingData, error) {
	util.PanicIf(bv.Operator != core.ValueOptEQ, "default container support EQ operator only")
	values, e := h.GetTokenizer(field.Field).TokenizeValue(bv.Value)
	if e != nil {
		return nil, fmt.Errorf("field:%s value:%+v parse fail, err:%s", field.Field, bv, e.Error())
	}
	return &StrTokenData{StrListValues: cache.StrListValues{Values: values}}, nil
}

func (h *CompressedKVBuilder) CommitFieldIndexingData(tx core.FieldIndexingData) error {
	if tx.Data == nil {
		return nil
	}

	if h.builder == nil {
		return fmt.Errorf("builder is nil (already compiled?)")
	}

	data := tx.Data.(*StrTokenData)
	values := util.DistinctString(data.StrListValues.Values)

	for _, value := range values {
		h.builder.AddEntryWithFieldID(tx.Field.ID, value, tx.EID)
	}
	return nil
}

// ------------------------------------------------------------------------------------------------
// CompressedKVIndex Implementation
// ------------------------------------------------------------------------------------------------

func (h *CompressedKVIndex) GetTokenizer(field core.BEField) parser.ValueTokenizer {
	if p, ok := h.fieldParser[field]; ok {
		return p
	}
	return parser.NewDefaultTokenizer()
}

func (h *CompressedKVIndex) DumpInfo(buffer *strings.Builder) {
	summary := map[string]any{
		"name": "CompressedKVIndex(Segment)",
		"mode": "segment_phase2",
	}
	if h.reader != nil {
		summary["dictSize"] = h.reader.dict.Size()
		summary["postings"] = len(h.reader.postings)
		summary["dataSize"] = len(h.segmentData)
	}

	for field := range h.fieldParser {
		summary[fmt.Sprintf("field#%s#parser", field)] = "custom"
	}
	buffer.WriteString(util.JSONPretty(summary))
}

func (h *CompressedKVIndex) GetEntries(field *core.FieldDesc, assigns core.Values) (r []core.PostingIterator, e error) {
	var values []string
	if values, e = h.GetTokenizer(field.Field).TokenizeAssign(assigns); e != nil {
		return nil, e
	}

	if h.reader == nil {
		// Not compiled yet or empty
		return nil, nil
	}

	for _, value := range values {
		iter, err := h.reader.GetPostingsWithFieldID(field.ID, field.Field, value)
		if err != nil {
			continue
		}
		if iter != nil {
			r = append(r, iter)
		}
	}
	return r, nil
}

func (h *CompressedKVIndex) Serialize() ([]byte, error) {
	if h.segmentData == nil {
		return nil, fmt.Errorf("no data to serialize")
	}
	return h.segmentData, nil
}

func (h *CompressedKVIndex) Deserialize(data []byte) error {
	if len(data) == 0 {
		h.reader = nil
		h.segmentData = nil
		return nil
	}

	reader, err := NewBlockSegmentReader(data)
	if err != nil {
		return err
	}

	h.reader = reader
	h.segmentData = data
	return nil
}
