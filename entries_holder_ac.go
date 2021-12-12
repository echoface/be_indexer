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
		builder *acMatcherBuilder
		matcher *cedar.Matcher
		debug   bool
	}
)

func newAcHolderBuilder() *acMatcherBuilder {
	return &acMatcherBuilder{
		values: make(map[string]Entries),
	}
}

func NewACEntriesHolder() EntriesHolder {
	return &ACEntriesHolder{
		builder: newAcHolderBuilder(),
		matcher: cedar.NewMatcher(),
	}
}

func (h *ACEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

func (h *ACEntriesHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("\n>>>>full entries >>>>>>>>>>>>>>>>>>>>>\n")
	if h.builder != nil {
		buffer.WriteString("ACMatchHolder drop detail for memory reason\n")
		return
	}
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
			// return nil, fmt.Errorf("field:%s need string value, but query value is:%+v", field.Field, value)
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
			Logger.Debugf("field:%s get entries by key:%s", field.Field, key)
			cursors = append(cursors, NewEntriesCursor(newQKey(field.Field, key), itr.Value.(Entries)))
		}
	}
	return cursors, nil
}

func (h *ACEntriesHolder) CompileEntries() {
	for term, entries := range h.builder.values {
		//var total int64
		sort.Sort(entries)
		//if kse.maxLen < int64(len(entries)) {
		//	kse.maxLen = int64(len(entries))
		//}
		//total += int64(len(entries))
		//if len(kse.plEntries) > 0 {
		//	kse.avgLen = total / int64(len(kse.plEntries))
		//}
		h.matcher.Insert([]byte(term), entries)
	}
	h.matcher.Compile()
	h.builder = nil
}
