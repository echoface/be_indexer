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

		// IndexingBETx holder tokenize/parse values into what its needed data
		// then wait IndexerBuilder call CommitAppend to apply 'Data' into holder
		// when all expression prepare success in a conjunction
		IndexingBETx(field *FieldDesc, bv *BoolValues) (TxData, error)

		// CommitIndexingBETx NOTE: builder will panic when error return,
		// because partial success for a conjunction will cause logic error
		CommitIndexingBETx(tx IndexingBETx) error

		// DecodeTxData decode data; used for building progress cache
		DecodeTxData(data []byte) (TxData, error)

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries() error
	}

	TxData interface {
		// BetterToCache: if txData big enaough, perfer to cache it; builder will
		// detect all expressions in a conjunction and decide wheather cache it or not
		BetterToCache() bool

		// Encode, searialize TxData for cacheing
		Encode() ([]byte, error)
	}

	IndexingBETx struct {
		field  *FieldDesc
		holder EntriesHolder

		eid  EntryID
		data TxData
	}

	// DefaultEntriesHolder EntriesHolder implement base on hash map holder map<key, Entries>
	// 默认容器,目前支持表达式最大256个field; 支持多个field复用默认容器; 见:Key编码逻辑
	// 如果需要打破这个限制,可以自己实现容器.
	DefaultEntriesHolder struct {
		debug     bool
		maxLen    int64 // max length of Entries
		avgLen    int64 // avg length of Entries
		plEntries map[Key]Entries
	}

	Uint64TxData cache.Uint64ListValues
)

var (
	BetterToCacheMaxItemsCount = 512
)

func NewDefaultEntriesHolder() *DefaultEntriesHolder {
	return &DefaultEntriesHolder{
		plEntries: map[Key]Entries{},
	}
}

func (txd *Uint64TxData) BetterToCache() bool {
	return len(txd.Values) > BetterToCacheMaxItemsCount
}

func (txd *Uint64TxData) Encode() ([]byte, error) {
	protoMsg := (*cache.Uint64ListValues)(txd)
	return proto.Marshal(protoMsg)
}

// DecodeTxData decode data; used for building progress cache
func (h *DefaultEntriesHolder) DecodeTxData(data []byte) (TxData, error) {
	if len(data) == 0 {
		return &Uint64TxData{Values: nil}, nil
	}
	txData := &Uint64TxData{}
	err := proto.Unmarshal(data, (*cache.Uint64ListValues)(txData))
	return txData, err
}

func (h *DefaultEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *DefaultEntriesHolder) DumpInfo(buffer *strings.Builder) {
	infos := map[string]interface{}{
		"name":          "ExtendLgtHolder",
		"kvCnt":         len(h.plEntries),
		"maxEntriesLen": h.maxLen,
		"avgEntriesLen": h.avgLen,
	}
	buffer.WriteString(util.JSONPretty(infos))
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

func (h *DefaultEntriesHolder) GetParser() parser.FieldValueParser {
	return nil
}

func (h *DefaultEntriesHolder) CompileEntries() error {
	h.makeEntriesSorted()
	return nil
}

func (h *DefaultEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
	var ids []uint64

	if ids, e = field.Parser.ParseAssign(assigns); e != nil {
		return nil, e
	}
	for _, id := range ids {

		key := NewKey(field.ID, id)

		if entries := h.getEntries(key); len(entries) > 0 {
			r = append(r, NewEntriesCursor(newQKey(field.Field, id), entries))
		}
	}
	return r, nil
}

func (h *DefaultEntriesHolder) IndexingBETx(field *FieldDesc, bv *BoolValues) (TxData, error) {
	util.PanicIf(bv.Operator != ValueOptEQ, "default container support EQ operator only")

	// NOTE: ids can be replicated if expression contain cross condition
	ids, e := field.Parser.ParseValue(bv.Value)
	if e != nil {
		return nil, fmt.Errorf("field:%s value:%+v parse fail, err:%s", field.Field, bv, e.Error())
	}
	return &Uint64TxData{Values: ids}, nil
}

func (h *DefaultEntriesHolder) CommitIndexingBETx(tx IndexingBETx) error {
	if tx.data == nil {
		return nil
	}

	var ok bool
	var data *Uint64TxData
	if data, ok = tx.data.(*Uint64TxData); !ok {
		panic(fmt.Errorf("bad preparation.Data type, oops..."))
	}
	values := util.DistinctInteger(data.Values)
	for _, id := range values {
		key := NewKey(tx.field.ID, id)
		h.plEntries[key] = append(h.plEntries[key], tx.eid)
	}
	return nil
}

func (h *DefaultEntriesHolder) getEntries(key Key) Entries {
	if entries, hit := h.plEntries[key]; hit {
		return entries
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
