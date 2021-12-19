package be_indexer

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	cedar "github.com/iohub/ahocorasick"
)

type (
	acMatcherBuilder struct {
		values map[string]Entries
	}

	ACEntriesHolder struct {
		builder     *acMatcherBuilder
		matcher     *cedar.Matcher
		debug       bool
		KeepBuilder bool
		totalTokens int
		maxLen      int64 // max length of Entries
		avgLen      int64 // avg length of Entries
	}
)

func newAcHolderBuilder() *acMatcherBuilder {
	return &acMatcherBuilder{
		values: make(map[string]Entries),
	}
}

// NewACEntriesHolder it will default drop the builder after compile ac-machine,
// you can register a customized ACEntriesHolder(with builder detail), and the register it
// RegisterEntriesHolder(HolderNameACMatcher, func() EntriesHolder {
//     holder := NewACEntriesHolder()
//     holder.KeepBuilder = true
//     return holder
// })
// NOTE: this just for debugging usage, it will consume memory much more
func NewACEntriesHolder() *ACEntriesHolder {
	return &ACEntriesHolder{
		builder: newAcHolderBuilder(),
		matcher: cedar.NewMatcher(),
	}
}

func (h *ACEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

func (h *ACEntriesHolder) DumpEntries(buffer *strings.Builder) {
	if h.builder != nil {
		buffer.WriteString("ACMatchHolder drop detail for memory reason\n")
		return
	}
	buffer.WriteString("ACMatchHolder origin keywords dict:\n")
	for key, entries := range h.builder.values {
		buffer.WriteString(key)
		buffer.WriteString(":")
		buffer.WriteString(strings.Join(entries.DocString(), ","))
		buffer.WriteString("\n")
	}
}

func (h *ACEntriesHolder) AddFieldEID(field *FieldDesc, values Values, eid EntryID) error {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			h.builder.values[v] = append(h.builder.values[v], eid)
		case []byte:
			h.builder.values[string(v)] = append(h.builder.values[string(v)], eid)
		default:
			return fmt.Errorf("field:%s need string value, but it's not:%+v", field.Field, value)
		}
	}
	return nil
}

func (h *ACEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (CursorGroup, error) {
	buf := bytes.NewBuffer(nil)
	for _, assign := range assigns {
		switch v := assign.(type) {
		case []byte:
			buf.Write(v)
		case string:
			buf.WriteString(v)
		default:
			Logger.Errorf("field:%s query assign need string type, but:%+v", field.Field, assign)
		}
	}
	if buf.Len() == 0 {
		return nil, nil
	}

	var cursors CursorGroup

	resp := h.matcher.Match(buf.Bytes())
	for resp.HasNext() {
		items := resp.NextMatchItem(buf.Bytes())
		for _, itr := range items {
			key := h.matcher.Key(buf.Bytes(), itr)
			cursors = append(cursors, NewEntriesCursor(newQKey(field.Field, string(key)), itr.Value.(Entries)))
		}
	}
	return cursors, nil
}

func (h *ACEntriesHolder) CompileEntries() {

	var total int64
	for term, entries := range h.builder.values {
		sort.Sort(entries)

		if h.maxLen < int64(len(entries)) {
			h.maxLen = int64(len(entries))
		}
		total += int64(len(entries))

		h.matcher.Insert([]byte(term), entries)
	}
	if len(h.builder.values) > 0 {
		h.totalTokens = len(h.builder.values)
		h.avgLen = total / int64(h.totalTokens)
	}

	h.matcher.Compile()

	if !h.KeepBuilder {
		h.builder = nil
	}
}
