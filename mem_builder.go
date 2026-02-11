package be_indexer

import (
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"github.com/echoface/be_indexer/core"

	"github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

// MemSegmentBuilder 基于内存的 Segment 构建器
// 对应原来的 CompressedKVHolder 的写入部分
type MemSegmentBuilder struct {
	buf         []termEID
	fieldParser map[core.BEField]parser.ValueTokenizer
	docCount    int
}

type termEID struct {
	term KVTerm
	eid  core.EntryID
}

func NewMemSegmentBuilder() *MemSegmentBuilder {
	return &MemSegmentBuilder{
		buf:         make([]termEID, 0, 1024),
		fieldParser: make(map[core.BEField]parser.ValueTokenizer),
	}
}

func (b *MemSegmentBuilder) RegisterFieldTokenizer(field core.BEField, tokenizer parser.ValueTokenizer) {
	if tokenizer == nil {
		delete(b.fieldParser, field)
		return
	}
	b.fieldParser[field] = tokenizer
}

func (b *MemSegmentBuilder) getTokenizer(field core.BEField) parser.ValueTokenizer {
	if p, ok := b.fieldParser[field]; ok {
		return p
	}
	return parser.NewDefaultTokenizer()
}

func (b *MemSegmentBuilder) AddDocument(doc *core.Document) error {
	b.docCount++
	// 遍历文档的所有 core.Conjunction
	for _, conj := range doc.Cons {
		// 遍历 core.Conjunction 的所有表达式
		for field, exprs := range conj.Predicates {
			for _, expr := range exprs {
				// 目前仅支持 EQ
				// TODO: Phase 2/3 支持 Range 等其他谓词
				if expr.Operator != core.ValueOptEQ {
					continue
				}

				// Tokenize
				values, err := b.getTokenizer(core.BEField(field)).TokenizeValue(expr.Value)
				if err != nil {
					return fmt.Errorf("tokenize field %s failed: %v", field, err)
				}
				values = util.DistinctString(values)

				// 生成 core.EntryID (这里简化逻辑，假设 core.EntryID 已经在 core.Document 中分配好，
				// 但实际上 core.Document 只有 core.Conjunction ID。
				// 在旧的逻辑中，EntryID 是在 IndexerBuilder 中分配的。
				// Phase 1 我们先假设外部已经处理好 core.EntryID 的生成，或者我们在 Builder 内部处理？
				// 回看 entries_holder_default.go，它接受的是 core.FieldIndexingData，其中包含 EID。
				// IndexerBuilder.AddDocument -> processDocument -> 此时分配 EID。
				// 既然 SegmentBuilder 替代的是 Holder，那么它应该接收 "Field/Value -> EID" 的映射。
				// 但 AddDocument 接口是高级接口。
				// 为了简单起见，我们暂且保留原 Holder 的逻辑：Document 已经被拆解。
				// 但这里 SegmentBuilder.AddDocument 接收的是 *core.Document。
				// 这意味着我们需要在 AddDocument 内部复现 processDocument 的部分逻辑（生成 core.EntryID）。
				// 但 core.EntryID 的生成依赖全局的 IDAllocator。
				// **修正**: 为了兼容现有架构，Phase 1 的 SegmentBuilder 最好提供更底层的 AddEntry 接口，
				// 或者让 IndexerBuilder 依然负责分配 ID。
			}
		}
	}
	return nil
}

// AddEntry 是更底层的接口，直接添加 core.Term -> core.EntryID 的映射
// 供 IndexerBuilder 使用
func (b *MemSegmentBuilder) AddEntry(field core.BEField, value interface{}, eid core.EntryID) error {
	// Tokenize value
	strValues, err := b.getTokenizer(field).TokenizeValue(value)
	if err != nil {
		return err
	}
	strValues = util.DistinctString(strValues)

	// fid := uint64(0)
	// 在原有的 Holder 中，FieldDesc 包含 ID。
	// 这里为了简化，我们假设 value 已经是处理好的，或者我们需要 FieldID 映射。
	// 暂时先用 0，Phase 2 会引入 FieldName -> FieldID 的映射。
	// 实际上 KVTerm 需要 FieldID。
	// 让我们回看 NewKVTerm。目前的实现需要 uint64 FieldID。
	// 如果我们不在 Builder 里维护 Field 字典，就得由调用方传进来。
	// 为了推进，我们让 AddEntry 接受 FieldID。
	return fmt.Errorf("use AddEntryWithFieldID instead")
}

func (b *MemSegmentBuilder) AddEntryWithFieldID(fieldID uint64, value string, eid core.EntryID) {
	b.buf = append(b.buf, termEID{
		term: NewKVTerm(fieldID, value),
		eid:  eid,
	})
}

func (b *MemSegmentBuilder) Reset() {
	b.buf = b.buf[:0]
	b.docCount = 0
}

// Flush 将 buffer 排序、压缩并写入 output
func (b *MemSegmentBuilder) Flush(output io.Writer) error {
	// 1. Sort
	if len(b.buf) > 0 {
		sort.Slice(b.buf, func(i, j int) bool {
			ti, tj := b.buf[i].term, b.buf[j].term
			if ti.FieldID != tj.FieldID {
				return ti.FieldID < tj.FieldID
			}
			if ti.Value != tj.Value {
				return ti.Value < tj.Value
			}
			return b.buf[i].eid < b.buf[j].eid
		})
	}

	// 2. Build core.Term Dictionary & Postings
	// Phase 2: Use core.Term Dictionary instead of CompressedFlatMap
	dictBuilder := NewVellumTermDictBuilder()

	// Group entries by core.Term
	var (
		curTerm  KVTerm
		cur      []uint64
		postings []*indexstore.PostingPtr
		headers  []*indexstore.FlatMapHeader
		pData    []byte
	)
	cur = make([]uint64, 0, 64)

	// Create a temporary builder for posting data compression
	// We reuse FlatMapBuilder logic but only for data compression part (headers + data)
	// Actually we need to manually manage headers and data since we are splitting them

	// Re-implement simplified compression logic here
	// Or use a helper.
	// To keep it simple, let's manually compress posting lists and append to pData

	flushTerm := func() error {
		if len(cur) == 0 {
			return nil
		}

		// 1. Add to Dictionary
		if err := dictBuilder.Add(curTerm); err != nil {
			return err
		}
		// core.TermID is the index in dictBuilder.terms - 1

		// 2. Compress Posting List
		// We use a small block size for granularity
		blockSize := util.DefaultBlockSize

		startOffset := uint32(len(headers))

		// Split into blocks
		for i := 0; i < len(cur); i += blockSize {
			end := i + blockSize
			if end > len(cur) {
				end = len(cur)
			}
			block := cur[i:end]

			// Compress block
			// Format: [FirstID(Uvarint)] [Delta(Uvarint)]...
			// Note: This duplicates logic in FlatMapBuilder.
			// Ideally we should extract a PostingListCompressor.

			// Let's implement inline for now
			base := block[0]
			var buf [binary.MaxVarintLen64]byte
			n := binary.PutUvarint(buf[:], base)

			// header offset points to start of this block in pData
			header := &indexstore.FlatMapHeader{
				LastEntryId: block[len(block)-1],
				Offset:      uint32(len(pData)),
			}
			headers = append(headers, header)

			pData = append(pData, buf[:n]...)

			for j := 1; j < len(block); j++ {
				delta := block[j] - base
				base = block[j]
				n = binary.PutUvarint(buf[:], delta)
				pData = append(pData, buf[:n]...)
			}
		}

		// 3. Create PostingPtr
		postings = append(postings, &indexstore.PostingPtr{
			HeaderOffset: startOffset,
			Length:       uint32(len(cur)),
		})

		cur = cur[:0]
		return nil
	}

	for _, p := range b.buf {
		if len(cur) == 0 {
			curTerm = p.term
		} else if p.term != curTerm {
			if err := flushTerm(); err != nil {
				return err
			}
			curTerm = p.term
		}
		cur = append(cur, uint64(p.eid))
	}
	if err := flushTerm(); err != nil {
		return err
	}

	// 3. Build Dict Proto
	termDict, err := dictBuilder.Build()
	if err != nil {
		return err
	}

	// 4. Serialize SegmentData
	segData := &indexstore.SegmentData{
		Dict:        termDict,
		Postings:    postings,
		Headers:     headers,
		PostingData: pData,
		BlockSize:   uint32(util.DefaultBlockSize),
	}

	data, err := proto.Marshal(segData)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
}
