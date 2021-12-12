package be_indexer

import (
	"fmt"
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
		//GetEntries(field *FieldDesc, assigns Values) (FieldScanner, error)

		// AddFieldEID(field *FieldDesc, value interface{}, collector Collector) (error)

		// AddFieldEID tokenize values and add it to holder container
		AddFieldEID(field *FieldDesc, values Values, eid EntryID) error
	}

	// DefaultEntriesHolder EntriesHolder implement base on hash map holder map<key, Entries>
	DefaultEntriesHolder struct {
		PostingEntries
		debug bool
	}
)

func NewDefaultEntriesHolder() EntriesHolder {
	return &DefaultEntriesHolder{
		PostingEntries: PostingEntries{
			plEntries: map[Key]Entries{},
		},
	}
}

func (h *DefaultEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

func (h *DefaultEntriesHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("\n>>>>full entries >>>>>>>>>>>>>>>>>>>>>\n")
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
	// NOTE: ids may replicate if expression contain cross condition
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
