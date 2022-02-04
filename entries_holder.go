package be_indexer

import (
	"fmt"
	"sort"
	"strings"
)

type (
	EntriesHolder interface {
		EnableDebug(debug bool)

		DumpEntries(buffer *strings.Builder)

		// CompileEntries finalize entries status for query, build or make sorted
		// according to the paper, entries must be sorted
		CompileEntries()

		GetEntries(field *FieldDesc, assigns Values) (CursorGroup, error)
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

func (h *DefaultEntriesHolder) DumpEntries(buffer *strings.Builder) {
	for key, entries := range h.plEntries {
		buffer.WriteString(key.String())
		buffer.WriteString(":")
		buffer.WriteString(strings.Join(entries.DocString(), ","))
		buffer.WriteString("\n")
	}
}

func (h *DefaultEntriesHolder) CompileEntries() {
	h.makeEntriesSorted()
}

func (h *DefaultEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (r CursorGroup, e error) {
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
			h.AppendEntryID(NewKey(field.ID, id), eid)
		}
	}
	return nil
}

func (kse *DefaultEntriesHolder) AppendEntryID(key Key, id EntryID) {
	entries, hit := kse.plEntries[key]
	if !hit {
		kse.plEntries[key] = Entries{id}
	}
	entries = append(entries, id)
	kse.plEntries[key] = entries
}

func (kse *DefaultEntriesHolder) getEntries(key Key) Entries {
	if entries, hit := kse.plEntries[key]; hit {
		return entries
	}
	return nil
}

func (kse *DefaultEntriesHolder) makeEntriesSorted() {
	var total int64
	for _, entries := range kse.plEntries {
		sort.Sort(entries)
		if kse.maxLen < int64(len(entries)) {
			kse.maxLen = int64(len(entries))
		}
		total += int64(len(entries))
	}
	if len(kse.plEntries) > 0 {
		kse.avgLen = total / int64(len(kse.plEntries))
	}
}
