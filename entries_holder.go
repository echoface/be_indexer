package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/codegen/cache"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	// EntriesHolder 存储索引的PostingList数据
	// 目前的三种典型场景:
	// 1. 内存KV 存储所有Field值对应的EntryID列表(PostingList)
	// 2. AC自动机：用于将所有的values 构建生成AC自动机，对输入的语句找到匹配的PostingList
	// 3. 1的一种扩展，引入网络\磁盘存储，内部维护一个LRU/LFU cache减轻内存压力
	EntriesHolder interface {
		EnableDebug(debug bool)

		DumpInfo(buffer *strings.Builder)

		DumpEntries(buffer *strings.Builder)

		// GetEntries retrieve all satisfied PostingList from holder
		GetEntries(field *FieldDesc, assigns Values) (EntriesCursors, error)

		// BuildFieldIndexingData holder tokenize/parse values into what its needed data
		// then wait IndexerBuilder call CommitFieldIndexingData to apply 'Data' into holder
		// when all expression prepare success in a conjunction
		BuildFieldIndexingData(field *FieldDesc, bv *BoolValues) (IndexingData, error)

		// CommitFieldIndexingData NOTE: builder will panic when error return,
		// because partial success for a conjunction will cause logic error
		CommitFieldIndexingData(tx FieldIndexingData) error

		// DecodeFieldIndexingData decode data; used for building progress cache
		DecodeFieldIndexingData(data []byte) (IndexingData, error)

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries() error
	}

	// holder 自定义的索引数据，encode用于支持增量构建
	IndexingData interface {
		// Encode serialize TxData for caching
		Encode() ([]byte, error)
	}

	FieldIndexingData struct {
		field  *FieldDesc
		holder EntriesHolder

		EID  EntryID
		Data IndexingData
	}

	Term struct {
		FieldID uint64
		Value   string
	}

	// DefaultEntriesHolder EntriesHolder implement base on hash map holder map<key, Entries>
	// 默认容器,目前支持表达式最大256个field; 支持多个field复用默认容器; 见:Key编码逻辑
	// 如果需要打破这个限制,可以自己实现容器.
	DefaultEntriesHolder struct {
		debug       bool
		maxLen      int64 // max length of Entries
		avgLen      int64 // avg length of Entries
		plEntries   map[Term]Entries
		fieldParser map[BEField]parser.ValueTokenizer
	}

	StrTokenData struct {
		cache.StrListValues
	}
)

func NewTerm(fid uint64, value string) Term {
	return Term{FieldID: fid, Value: value}
}

func (tm Term) String() string {
	return fmt.Sprintf("<%d,%s>", tm.FieldID, tm.Value)
}

func NewDefaultEntriesHolder() *DefaultEntriesHolder {
	return &DefaultEntriesHolder{
		plEntries:   make(map[Term]Entries),
		fieldParser: make(map[BEField]parser.ValueTokenizer),
	}
}

func (std *StrTokenData) Encode() ([]byte, error) {
	return proto.Marshal(&std.StrListValues)
}

// DecodeFieldIndexingData decode data; used for building progress cache
func (h *DefaultEntriesHolder) DecodeFieldIndexingData(data []byte) (IndexingData, error) {
	if len(data) == 0 {
		return &StrTokenData{}, nil
	}
	txData := &StrTokenData{}
	err := proto.Unmarshal(data, &txData.StrListValues)
	return txData, err
}

func (h *DefaultEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *DefaultEntriesHolder) DumpInfo(buffer *strings.Builder) {
	summary := map[string]interface{}{
		"name":          HolderNameDefault,
		"termCnt":       len(h.plEntries),
		"maxEntriesLen": h.maxLen,
		"avgEntriesLen": h.avgLen,
	}
	for field := range h.fieldParser {
		summary[fmt.Sprintf("field#%s#parser", field)] = "custom"
	}
	buffer.WriteString(util.JSONPretty(summary))
}

func (h *DefaultEntriesHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("DefaultEntriesHolder entries:")
	for key, entries := range h.plEntries {
		buffer.WriteString("\n")
		buffer.WriteString(key.String())
		buffer.WriteString(":")
		buffer.WriteString(strings.Join(entries.DocString(), ","))
	}
}

func (h *DefaultEntriesHolder) GetTokenizer(field BEField) parser.ValueTokenizer {
	if p, ok := h.fieldParser[field]; ok {
		return p
	}
	return parser.NewDefaultTokenizer()
}

// RegisterFieldTokenizer registers a custom tokenizer for a specific field.
// If nil is passed, the default tokenizer will be used.
func (h *DefaultEntriesHolder) RegisterFieldTokenizer(field BEField, tokenizer parser.ValueTokenizer) {
	if tokenizer == nil {
		delete(h.fieldParser, field)
		return
	}
	h.fieldParser[field] = tokenizer
}

func (h *DefaultEntriesHolder) CompileEntries() error {
	h.makeEntriesSorted()
	return nil
}

func (h *DefaultEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
	var values []string
	if values, e = h.GetTokenizer(field.Field).TokenizeAssign(assigns); e != nil {
		return nil, e
	}
	for _, value := range values {
		key := NewTerm(field.ID, value)
		if entries, hit := h.plEntries[key]; hit && len(entries) > 0 {
			cursor := NewEntriesCursor(NewQKey(field.Field, value), entries)
			r = append(r, cursor)
		}
	}
	return r, nil
}

func (h *DefaultEntriesHolder) BuildFieldIndexingData(field *FieldDesc, bv *BoolValues) (IndexingData, error) {
	util.PanicIf(bv.Operator != ValueOptEQ, "default container support EQ operator only")

	// NOTE: values can be replicated if expression contain cross condition
	values, e := h.GetTokenizer(field.Field).TokenizeValue(bv.Value)
	if e != nil {
		return nil, fmt.Errorf("field:%s value:%+v parse fail, err:%s", field.Field, bv, e.Error())
	}
	return &StrTokenData{StrListValues: cache.StrListValues{Values: values}}, nil
}

func (h *DefaultEntriesHolder) CommitFieldIndexingData(tx FieldIndexingData) error {
	if tx.Data == nil {
		return nil
	}

	var ok bool
	data, ok := tx.Data.(*StrTokenData)
	util.PanicIf(!ok, "bad TxData need *StringTxData, oops")

	values := util.DistinctString(data.StrListValues.Values)
	for _, value := range values {
		key := NewTerm(tx.field.ID, value)
		h.plEntries[key] = append(h.plEntries[key], tx.EID)
	}
	return nil
}

func (h *DefaultEntriesHolder) makeEntriesSorted() {
	var total int64
	for _, entries := range h.plEntries {
		sort.Sort(entries)
		if h.maxLen < int64(len(entries)) {
			h.maxLen = int64(len(entries))
		}
		total += int64(len(entries))
	}
	if len(h.plEntries) > 0 {
		h.avgLen = total / int64(len(h.plEntries))
	}
}
