package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"fmt"

	indexstore "github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

// BlockSegmentReader 基于 FlatMap 的只读 Segment
// 对应原来的 CompressedKVHolder 的读取部分
type BlockSegmentReader struct {
	// Phase 2: Switch to TermDictionary based lookup
	dict        TermDictionary
	postings    []*indexstore.PostingPtr
	headers     []util.CompHeader
	postingData []byte

	fieldParser map[core.BEField]parser.ValueTokenizer
}

func NewBlockSegmentReader(data []byte) (*BlockSegmentReader, error) {
	// 1. Try to unmarshal as SegmentData (Phase 2 format)
	segData := &indexstore.SegmentData{}
	if err := proto.Unmarshal(data, segData); err == nil && segData.Dict != nil {
		// Loaded Phase 2 format
		headers := make([]util.CompHeader, 0, len(segData.Headers))
		for _, h := range segData.Headers {
			headers = append(headers, util.CompHeader{
				LastEntryID: h.LastEntryId,
				Offset:      h.Offset,
			})
		}

		return &BlockSegmentReader{
			dict:        NewTermDictFromProto(segData.Dict),
			postings:    segData.Postings,
			headers:     headers,
			postingData: segData.PostingData,
			fieldParser: make(map[core.BEField]parser.ValueTokenizer),
		}, nil
	}

	return nil, fmt.Errorf("failed to load segment: invalid or unsupported format (legacy support removed)")
}

func (r *BlockSegmentReader) RegisterFieldTokenizer(field core.BEField, tokenizer parser.ValueTokenizer) {
	if tokenizer == nil {
		delete(r.fieldParser, field)
		return
	}
	r.fieldParser[field] = tokenizer
}

func (r *BlockSegmentReader) getTokenizer(field core.BEField) parser.ValueTokenizer {
	if p, ok := r.fieldParser[field]; ok {
		return p
	}
	return parser.NewDefaultTokenizer()
}

func (r *BlockSegmentReader) GetPostings(field core.BEField, value interface{}) (core.PostingIterator, error) {
	// Phase 1: 暂时假设外部已经知道 FieldID，或者我们在 Reader 里不处理 FieldID 查找？
	// Reader 的 GetPostings 接收的是 core.BEField (string) 和 value (any)。
	// 但 FlatMap 的 Key 是 KVTerm (uint64, string)。
	// 问题：这里缺少 FieldName -> FieldID 的映射。
	// 在 DefaultKVHolder 中，它是通过 core.FieldDesc 传递进来的。
	// 这里我们需要改变一下接口签名？或者在 Reader 内部维护 Metadata。
	// 既然 Phase 1 还没引入完整的 Schema 管理，我们暂时要求 value 必须能被 Tokenize 为 string，
	// 并且我们需要 FieldID。

	// 临时方案：GetPostingsWithFieldID
	return nil, fmt.Errorf("use GetPostingsWithFieldID instead")
}

func (r *BlockSegmentReader) GetPostingsWithFieldID(fieldID uint64, field core.BEField, value interface{}) (core.PostingIterator, error) {
	// 1. Tokenize
	strValues, err := r.getTokenizer(field).TokenizeValue(value)
	if err != nil {
		return nil, err
	}
	if len(strValues) == 0 {
		return nil, nil
	}

	// 简单起见，取第一个 value (Match Query 通常是一个值)
	targetVal := strValues[0]

	// 2. Lookup in core.Term Dictionary
	termID, found := r.dict.Get(fieldID, targetVal)
	if !found {
		return nil, nil
	}

	// 3. Retrieve PostingPtr
	if int(termID) >= len(r.postings) {
		return nil, fmt.Errorf("termID %d out of bounds (postings len: %d)", termID, len(r.postings))
	}
	ptr := r.postings[termID]

	// 4. Create BlockIterator
	headerStart := int(ptr.HeaderOffset)
	// Calculate headerEnd based on length? No, HeaderOffset + number of blocks needed.
	// Actually, BlockIterator logic is: iterate until headerEnd.
	// But ptr.Length is total ENTRIES count.
	// How many blocks? We don't know exactly unless we scan headers or store block count.
	// The original CompressedFlatMap logic used next key's header offset to determine end.

	headerEnd := len(r.headers)
	if int(termID)+1 < len(r.postings) {
		headerEnd = int(r.postings[termID+1].HeaderOffset)
	}

	cursor := NewBlockIterator(
		NewTerm(field, targetVal), // 构造一个 Search core.Term 用于调试
		r.headers,
		r.postingData,
		headerEnd,
	)

	if headerStart < headerEnd {
		cursor.LoadBlock(headerStart)
	}
	return cursor, nil
}

func (r *BlockSegmentReader) GetPostingsByTermID(termID uint64) (core.PostingIterator, error) {
	if int(termID) >= len(r.postings) {
		return nil, fmt.Errorf("termID %d out of bounds (postings len: %d)", termID, len(r.postings))
	}
	ptr := r.postings[termID]

	headerStart := int(ptr.HeaderOffset)
	headerEnd := len(r.headers)
	if int(termID)+1 < len(r.postings) {
		headerEnd = int(r.postings[termID+1].HeaderOffset)
	}

	// For dump purpose, we construct a dummy term.
	// We could decode the term from dict if we want accuracy, but this method is low-level.
	// The caller (DumpEntries) already knows the core.Term.
	cursor := NewBlockIterator(
		NewTerm("", ""),
		r.headers,
		r.postingData,
		headerEnd,
	)

	if headerStart < headerEnd {
		cursor.LoadBlock(headerStart)
	}
	return cursor, nil
}

func (r *BlockSegmentReader) Contains(id core.DocID) bool {
	// BlockSegmentReader 是部分索引，不包含完整的文档列表。
	// 通常 Contains 是由 Bitmap 或 BloomFilter 提供的。
	// Phase 1 暂时返回 true (假设不过滤)
	return true
}

func (r *BlockSegmentReader) Close() error {
	r.postingData = nil
	return nil
}
