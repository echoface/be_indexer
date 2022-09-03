package be_indexer

import (
	"fmt"
	"sort"
	"strings"
)

type (
	// EntriesHolder 存储索引的PostingList数据
	// 目前的三种典型场景:
	// 1. 内存KV 存储所有Field值对应的EntryID列表(PostingList)
	// 2. AC自动机：用于将所有的values 构建生成AC自动机，对输入的语句找到匹配的PostingList
	// 3. 1的一种扩展，引入网络存储，内部维护一个LRU/LFU cache减轻内存压力
	EntriesHolder interface {
		EnableDebug(debug bool)

		DumpInfo(buffer *strings.Builder)

		DumpEntries(buffer *strings.Builder)

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries() error

		GetEntries(field *FieldDesc, assigns Values) (EntriesCursors, error)
		//GetEntries(field *FieldDesc, assigns Values) (FieldCursor, error)

		// AddFieldEID tokenize values and add it to holder container
		AddFieldEID(field *FieldDesc, values Values, eid EntryID) error
	}

	// DefaultEntriesHolder EntriesHolder implement base on hash map holder map<key, Entries>
	DefaultEntriesHolder struct {
		debug     bool
		maxLen    int64 // max length of Entries
		avgLen    int64 // avg length of Entries
		plEntries map[Key]Entries
	}
)

func NewDefaultEntriesHolder() EntriesHolder {
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
	info := fmt.Sprintf("{name: %s, value_count:%d max_entries:%d avg_entries:%d}",
		"default", len(h.plEntries), h.maxLen, h.avgLen)
	buffer.WriteString(info)
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

func (h *DefaultEntriesHolder) CompileEntries() error {
	h.makeEntriesSorted()
	return nil
}

func (h *DefaultEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
	var ids []uint64

	for _, vi := range assigns {
		if ids, e = field.Parser.ParseAssign(vi); e != nil {
			return nil, e
		}
		for _, id := range ids {

			key := NewKey(field.ID, id)

			if entries := h.getEntries(key); len(entries) > 0 {

				r = append(r, NewEntriesCursor(newQKey(field.Field, vi), entries))
			}
		}
	}
	return r, nil
}

func (h *DefaultEntriesHolder) AddFieldEID(field *FieldDesc, values Values, eid EntryID) (err error) {
	var ids []uint64
	// NOTE: ids can be replicated if expression contain cross condition
	for _, value := range values {
		if ids, err = field.Parser.ParseValue(value); err != nil {
			return fmt.Errorf("field:%s parser value:%+v fail, err:%s", field.Field, value, err.Error())
		}
		for _, id := range ids {
			key := NewKey(field.ID, id)
			h.plEntries[key] = append(h.plEntries[key], eid)
		}
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
