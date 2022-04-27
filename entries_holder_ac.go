package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	aho "github.com/anknown/ahocorasick"
)

type (
	ACHolderOption struct {
		// QuerySep 查询时，当存在多个值时，使用什么分隔符拼接多个查询字段来组成查询语句, 默认使用whitespace
		// 这是因为在语义上'空'更符合逻辑表达的正确性, 但这也会导致一些问题需要注意：
		QuerySep string
	}

	ACEntriesHolder struct {
		ACHolderOption
		debug       bool
		totalTokens int
		maxLen      int64 // max length of Entries
		avgLen      int64 // avg length of Entries

		values  map[string]Entries
		machine *aho.Machine // matcher     *cedar.Matcher
	}
)

// NewACEntriesHolder it will default drop the builder after compile ac-machine,
func NewACEntriesHolder(option ACHolderOption) *ACEntriesHolder {
	holder := &ACEntriesHolder{
		ACHolderOption: option,
		values:         map[string]Entries{},
		machine:        new(aho.Machine), // matcher: cedar.NewMatcher(), deprecated for bug reason
	}
	return holder
}

func (h *ACEntriesHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *ACEntriesHolder) DumpInfo(buffer *strings.Builder) {
	info := fmt.Sprintf("{name: %s, value_count:%d max_entries:%d avg_entries:%d}",
		"ac_holder", len(h.values), h.maxLen, h.avgLen)
	buffer.WriteString(info)
}

func (h *ACEntriesHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("ACMatchHolder origin keywords dict:")
	for key, entries := range h.values {
		buffer.WriteString("\n")
		buffer.WriteString(key)
		buffer.WriteString(":")
		buffer.WriteString(strings.Join(entries.DocString(), ","))
	}
}

func (h *ACEntriesHolder) AddFieldEID(field *FieldDesc, values Values, eid EntryID) error {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			h.values[v] = append(h.values[v], eid)
		case []byte:
			h.values[string(v)] = append(h.values[string(v)], eid)
		default:
			return fmt.Errorf("field:%s need string value, but it's not:%+v", field.Field, value)
		}
	}
	return nil
}

func (h *ACEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (EntriesCursors, error) {
	if len(h.values) == 0 {
		return nil, nil
	}
	buf := make([]rune, 0, 256)
	for _, assign := range assigns {
		switch v := assign.(type) {
		case string:
			buf = append(buf, []rune(v)...)
		default:
			Logger.Errorf("field:%s query assign need string type, but:%+v", field.Field, assign)
		}
		buf = append(buf, []rune(h.QuerySep)...)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	var cursors EntriesCursors

	terms := h.machine.MultiPatternSearch(buf, false)
	for _, term := range terms {
		key := string(term.Word)
		if pl, ok := h.values[key]; ok && len(pl) > 0 {
			cursors = append(cursors, NewEntriesCursor(newQKey(field.Field, key), pl))
		}
	}
	return cursors, nil
}

func (h *ACEntriesHolder) CompileEntries() error {

	var total int64
	keys := make([][]rune, 0, len(h.values))
	for term, entries := range h.values {

		keys = append(keys, []rune(term))

		sort.Sort(entries)

		if h.maxLen < int64(len(entries)) {
			h.maxLen = int64(len(entries))
		}
		total += int64(len(entries))
	}

	if len(h.values) > 0 {
		h.totalTokens = len(h.values)
		h.avgLen = total / int64(h.totalTokens)
	}
	if len(keys) == 0 {
		return nil
	}
	return h.machine.Build(keys)
}
