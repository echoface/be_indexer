package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
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

		// PrepareAppend holder tokenize/parse values into what its needed data
		// then wait IndexerBuilder call CommitAppend to apply 'Data' into holder
		// when all expression prepare success in a conjunction
		PrepareAppend(field *FieldDesc, values *BoolValues) (Preparation, error)

		// CommitAppend NOTE: if partial success for a conjunction will cause logic error
		// so for a Holder implement should panic it if any errors happen
		CommitAppend(preparation *Preparation, eid EntryID)

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries() error
	}

	// Preparation : a temp context use hold partital parsed data for a field,
	// it will commit to holder when all field(in a Conjunction) be parsed, holder can
	// save customized data into Preparation.Data then retrieve(use) it when CommitEntries called
	Preparation struct {
		entryID EntryID
		field   *FieldDesc
		holder  EntriesHolder

		Data interface{}
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
)

func NewDefaultEntriesHolder() *DefaultEntriesHolder {
	return &DefaultEntriesHolder{
		plEntries: map[Key]Entries{},
	}
}

func (h *DefaultEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *DefaultEntriesHolder) DumpInfo(buffer *strings.Builder) {
	infos := map[string]interface{} {
		"name": "ExtendLgtHolder",
		"kvCnt": len(h.plEntries),
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

func (h *DefaultEntriesHolder) PrepareAppend(field *FieldDesc, values *BoolValues) (r Preparation, e error) {
	util.PanicIf(values.Operator != ValueOptEQ, "default container support EQ operator only")

	var ids []uint64
	// NOTE: ids can be replicated if expression contain cross condition
	if ids, e = field.Parser.ParseValue(values.Value); e != nil {
		return r, fmt.Errorf("field:%s value:%+v parse fail, err:%s", field.Field, values, e.Error())
	}
	r.Data = ids
	return r, nil
}

// CommitAppend NOTE: if partial success for a conjunction will cause logic error
// so for a Holder implement should panic it if any errors happen
func (h *DefaultEntriesHolder) CommitAppend(preparation *Preparation, eid EntryID) {
	if preparation.Data == nil {
		return
	}

	var ok bool
	var ids []uint64
	if ids, ok = preparation.Data.([]uint64); !ok {
		panic(fmt.Errorf("bad preparation.Data type, oops..."))
	}
	values := util.DistinctInteger(ids)
	for _, id := range values {
		key := NewKey(preparation.field.ID, id)
		h.plEntries[key] = append(h.plEntries[key], eid)
	}
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
