package core

import (
	"fmt"
	"io"
	"reflect"
	"strings"
)

const (
	WildcardFieldName = BEField("_Z_")

	HolderNameDefault     = "default"
	HolderNameACMatcher   = "ac_matcher"
	HolderNameExtendRange = "ext_range"
)

var WildcardTerm = NewTerm(WildcardFieldName, 0)

type (
	// --------------------------------------------------------------------------------
	// From be_indexer.go
	// --------------------------------------------------------------------------------

	FieldOption struct {
		Container string // specify Entries holder for all tokenized value Entries
	}

	IndexerSettings struct {
		FieldConfig map[BEField]FieldOption
	}

	BEIndex interface {
		// Retrieve scan index data and retrieve satisfied document
		Retrieve(queries Assignments, opt ...IndexOpt) (DocIDList, error)

		// RetrieveWithCollector scan index data and retrieve satisfied document
		RetrieveWithCollector(Assignments, ResultCollector, ...IndexOpt) error

		DumpIndexInfo(sb *strings.Builder)

		Dump(w io.Writer) error

		Load(r io.Reader) error

		// Phase 1: 支持 Segment 架构
		// GetSegment 返回底层的 SegmentReader (如果支持)
		// 这是一个过渡接口，未来 retrieve 应该构建在 Segment 之上
		GetSegment() (SegmentReader, error)
	}

	// BEIndexBuilder is the interface for building a BEIndex.
	// It decouples the write-path (building) from the read-path (BEIndex).
	BEIndexBuilder interface {
		// AddWildcardEID appends a wildcard entry ID.
		AddWildcardEID(id EntryID)

		// SetFieldDesc configures the fields for the indexer.
		SetFieldDesc(fieldsData map[BEField]*FieldDesc)

		// NewContainer returns a valid EntriesContainerBuilder for size k.
		// Note: EntriesContainerBuilder is implementation specific, so we might need interface here
		// or use any/interface{} if we want to be strictly generic, but for now let's assume specific implementation
		// However, since EntriesContainerBuilder is NOT in core, we cannot return it here if we want to keep strictly to core.
		// The original code used *EntriesContainerBuilder.
		// If BEIndexBuilder is in core, it cannot return *EntriesContainerBuilder if that type is in be_indexer.
		// Let's check where EntriesContainerBuilder is defined. It's in be_indexer_kgroups.go.
		// This suggests BEIndexBuilder CANNOT be in core if it references EntriesContainerBuilder.
		// OR EntriesContainerBuilder should be in core.
		// OR BEIndexBuilder should return an interface.
		// I already moved EntriesContainerBuilder to core.
		NewContainer(k int) *EntriesContainerBuilder

		// CompileIndexer finalizes the index building process and returns the searcher.
		// This replaces the old compileIndexer() that operated in-place.
		CompileIndexer() (BEIndex, error)
	}

	FieldDesc struct {
		FieldOption

		ID    uint64
		Field BEField
	}

	// --------------------------------------------------------------------------------
	// From entries_holder.go
	// --------------------------------------------------------------------------------

	IndexingData interface {
		// Encode serialize TxData for caching
		Encode() ([]byte, error)
	}

	FieldIndexingData struct {
		Field  *FieldDesc
		Holder FieldIndexBuilder
		EID    EntryID
		Data   IndexingData
	}

	// FieldIndex 存储索引的PostingList数据
	// 目前的三种典型场景:
	// 1. 内存KV 存储所有Field值对应的EntryID列表(PostingList)
	// 2. AC自动机：对所有布尔表达为命中的文本构建AC自动机，对输入的语句查找PostingList
	FieldIndex interface {
		DumpInfo(buffer *strings.Builder)

		// GetEntries retrieve all satisfied PostingIterator from holder
		GetEntries(field *FieldDesc, assigns Values) ([]PostingIterator, error)

		// save indexing data into bytes file
		Serialize() ([]byte, error)

		Deserialize(data []byte) error
	}

	FieldIndexBuilder interface {
		// BuildFieldIndexingData holder tokenize/parse values into what its needed data
		// then wait IndexerBuilder call CommitFieldIndexingData to apply 'Data' into holder
		// when all expression prepare success in a conjunction
		BuildFieldIndexingData(field *FieldDesc, bv *ValueExpr) (IndexingData, error)

		// DecodeFieldIndexingData decode data; used for building progress cache
		DecodeFieldIndexingData(data []byte) (IndexingData, error)

		// CommitFieldIndexingData NOTE: builder will panic when error return,
		// because partial success for a conjunction will cause logic error
		CommitFieldIndexingData(tx FieldIndexingData) error

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries() (FieldIndex, error)
	}

	FieldBuilderFactory func() FieldIndexBuilder
	FieldIndexFactory   func() FieldIndex

	// FieldImplementation holds both builder and index factories for a specific field type.
	// This ensures consistency between build and query implementations.
	FieldImplementation struct {
		NewBuilder FieldBuilderFactory
		NewIndex   FieldIndexFactory
	}

	// --------------------------------------------------------------------------------
	// From result_collector.go
	// --------------------------------------------------------------------------------

	ResultCollector interface {
		Add(id DocID, conj ConjID)

		GetDocIDs() (ids DocIDList)

		GetDocIDsInto(ids *DocIDList)
	}

	// --------------------------------------------------------------------------------
	// From segment_types.go
	// --------------------------------------------------------------------------------

	// Segment 代表一个不可变的索引片段
	// 它通常包含一批文档的倒排索引数据
	Segment interface {
		// ID 返回 Segment 的唯一标识 (暂时用 string 或 uint64，Phase 3 细化)
		ID() uint64

		// NumDocs 返回该 Segment 包含的文档数量
		NumDocs() int

		// Reader 返回该 Segment 的查询接口
		Reader() (SegmentReader, error)

		// Close 释放资源
		Close() error
	}

	// SegmentBuilder 用于构建一个新的 Segment (Write Path)
	// 它是 Mutable 的，通常在内存中积累数据
	SegmentBuilder interface {
		// AddDocument 添加一个文档到构建器
		AddDocument(doc *Document) error

		// Flush 将内存中的数据冻结并序列化，写入 writer
		// 写入完成后，可以通过 NewSegmentReader 打开这部分数据
		Flush(output io.Writer) error

		// Reset 重置构建器状态，以便复用
		Reset()
	}

	// SegmentReader 用于对不可变的 Segment 进行查询 (Read Path)
	// 它应该是线程安全的
	SegmentReader interface {
		// GetPostings 获取指定 Field/Value 的倒排链迭代器
		// Phase 1: 依然使用 BEField 和 value any (会转为 Term)
		GetPostings(field BEField, value interface{}) (PostingIterator, error)

		// Contains 检查文档是否存在 (用于过滤删除的文档)
		Contains(id DocID) bool

		// Close 释放读取资源
		Close() error
	}

	// --------------------------------------------------------------------------------
	// From index_scanner.go
	// --------------------------------------------------------------------------------

	Term struct {
		Field BEField
		Value any
	}

	// TermIterator 表示单个 term 的 posting cursor。
	// 这是检索主循环的最小抽象单元（必须支持 SkipTo）。
	TermIterator interface {
		// Current 返回当前 EntryID；若到达结尾则返回 NULLENTRY。
		Current() EntryID

		// SkipTo 推进到 >= target 的位置，并返回当前 EntryID（或 NULLENTRY）。
		SkipTo(target EntryID) EntryID

		// Key 返回调试用的 query key（仅用于 dump/log）。
		Term() Term
	}

	// FieldIterator 表示同一 field 下多个 term iterator 的聚合（OR 语义）。
	// 行为等价于当前的 FieldCursor：对外暴露“最小当前值”，并可整体 SkipTo。
	FieldIterator interface {
		Current() EntryID
		SkipTo(target EntryID) EntryID
		ReachEnd() bool
	}

	// PostingIterator 作为对外更通用的命名，第一期与 TermIterator 等价。
	PostingIterator = TermIterator

	// --------------------------------------------------------------------------------
	// Retrieval Context & Options
	// --------------------------------------------------------------------------------

	RetrieveContext struct {
		DumpStepInfo bool

		Collector ResultCollector

		Assigns Assignments
	}

	IndexOpt func(ctx *RetrieveContext)

	// BEIndexLogger is the interface for logging within the indexer.
	BEIndexLogger interface {
		Debugf(format string, v ...interface{})
		Infof(format string, v ...interface{})
		Errorf(format string, v ...interface{})
	}
)

func NewTerm(field BEField, v interface{}) Term {
	key := Term{Field: field, Value: v}
	return key
}

func (key *Term) String() string {
	switch v := key.Value.(type) {
	case string:
		return fmt.Sprintf("[%s,%s]", key.Field, v)
	case int8, int16, int, int32, int64, uint8, uint16, uint, uint32, uint64:
		return fmt.Sprintf("[%s,%d]", key.Field, v)
	default:
		fmt.Println("unknown type", reflect.TypeOf(key.Value).String())
	}
	return fmt.Sprintf("[%s,%+v]", key.Field, key.Value)
}

func NewRetrieveCtx(ass Assignments, opts ...IndexOpt) RetrieveContext {
	ctx := RetrieveContext{}
	ctx.Assigns = ass
	for _, fn := range opts {
		fn(&ctx)
	}
	return ctx
}
