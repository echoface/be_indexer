package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/util"
)

type (
	EntriesWrap struct {
		Field     BEField
		plEntries Entries
	}
	// DefaultExLgtHolder EntriesHolder implement base on default holder extend support for LT/GT operator
	DefaultExLgtHolder struct {
		debug  bool
		maxLen int64 // max length of Entries
		avgLen int64 // avg length of Entries

		plEntries map[Key]Entries

		// all "field < number" expression store here(sorted)
		ltEntries []EntriesWrap

		// all "field > number" expression store here(sorted)
		gtEntries []EntriesWrap
	}
	prepareState struct {
		op       ValueOpt
		lgtValue int64
		valueIDs []uint64
	}
)

func NewDefaultExtLtGtHolder() *DefaultExLgtHolder {
	util.PanicIf(true, "WIP, don't use it now")
	return &DefaultExLgtHolder{
		plEntries: map[Key]Entries{},
	}
}

func (h *DefaultExLgtHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *DefaultExLgtHolder) DumpInfo(buffer *strings.Builder) {
	info := fmt.Sprintf("{name: %s, value_count:%d max_entries:%d avg_entries:%d}",
		"default", len(h.plEntries), h.maxLen, h.avgLen)
	buffer.WriteString(info)
}

func (h *DefaultExLgtHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("DefaultEntriesHolder entries:")
	for key, entries := range h.plEntries {
		buffer.WriteString("\n")
		buffer.WriteString(key.String())
		buffer.WriteString(":")
		buffer.WriteString(strings.Join(entries.DocString(), ","))
	}
}

func (h *DefaultExLgtHolder) CompileEntries() error {
	h.makeEntriesSorted()
	return nil
}

func (h *DefaultExLgtHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
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
	if len(ids) > 0 {
		minValudID, maxValudID := int64(ids[0]), int64(ids[0])
		for _, v := range ids {
			minValudID = util.MinInt64(minValudID, int64(v))
			maxValudID = util.MaxInt64(maxValudID, int64(v))
		}
	}

	return r, nil
}

func (h *DefaultExLgtHolder) PrepareAppend(field *FieldDesc, values *BoolValues) (r Preparation, e error) {
	// util.PanicIf(values.Operator != ValueOptEQ, "default container support EQ operator only")

	switch values.Operator {
	case ValueOptEQ: // NOTE: ids can be replicated if expression contain cross condition
		var ids []uint64
		if ids, e = field.Parser.ParseValue(values.Value); e != nil {
			return r, fmt.Errorf("field:%s value:%+v parse fail, err:%s", field.Field, values, e.Error())
		}
		r.Data = ids
	case ValueOptLT:

	case ValueOptGT:
	}
	return r, nil
}

// CommitAppend NOTE: if partial success for a conjunction will cause logic error
// so for a Holder implement should panic it if any errors happen
func (h *DefaultExLgtHolder) CommitAppend(preparation *Preparation, eid EntryID) {
	if preparation.Data == nil {
		return
	}

	var ok bool
	var ids []uint64
	if ids, ok = preparation.Data.([]uint64); !ok {
		panic(fmt.Errorf("bad preparation.Data type, oops..."))
	}

	for _, id := range ids {
		key := NewKey(preparation.field.ID, id)
		h.plEntries[key] = append(h.plEntries[key], eid)
	}
}

func (h *DefaultExLgtHolder) getEntries(key Key) Entries {
	if entries, hit := h.plEntries[key]; hit {
		return entries
	}
	return nil
}

func (h *DefaultExLgtHolder) makeEntriesSorted() {
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
