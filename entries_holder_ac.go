package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/util"

	aho "github.com/anknown/ahocorasick"
)

type (
	acMatcherBuilder struct {
		values map[string]Entries
	}

	ACHolderOption struct {
		// QuerySep 查询时，当存在多个值时，使用什么分隔符拼接多个查询字段来组成查询语句, 默认使用whitespace
		// 这是因为在语义上'空'更符合逻辑表达的正确性
		QuerySep string
	}

	ACEntriesHolder struct {
		ACHolderOption

		builder *acMatcherBuilder
		// matcher     *cedar.Matcher
		machine     *aho.Machine
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
func NewACEntriesHolder(option ACHolderOption) *ACEntriesHolder {
	holder := &ACEntriesHolder{
		ACHolderOption: option,
		builder:        newAcHolderBuilder(),
		machine:        new(aho.Machine),
		// matcher: cedar.NewMatcher(), deprecated for bug reason
	}
	return holder
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

func (h *ACEntriesHolder) AddFieldEID(field *fieldDesc, values Values, eid EntryID) error {
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

func (h *ACEntriesHolder) GetEntries(field *fieldDesc, assigns Values) (CursorGroup, error) {
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

	var cursors CursorGroup

	terms := h.machine.MultiPatternSearch(buf, false)
	for _, term := range terms {
		key := string(term.Word)
		if pl, ok := h.builder.values[key]; ok && len(pl) > 0 {
			cursors = append(cursors, NewEntriesCursor(newQKey(field.Field, key), pl))
		}
	}
	/*
		resp := h.matcher.Match(buf.Bytes())
		defer resp.Release()
		for resp.HasNext() {
			items := resp.NextMatchItem(buf.Bytes())
			for _, itr := range items {
				key := h.matcher.Key(buf.Bytes(), itr)
				cursors = append(cursors, NewEntriesCursor(newQKey(field.Field, string(key)), itr.Value.(Entries)))
			}
		}
	*/
	return cursors, nil
}

func (h *ACEntriesHolder) CompileEntries() {

	var total int64
	keys := make([][]rune, 0, len(h.builder.values))
	for term, entries := range h.builder.values {

		keys = append(keys, []rune(term))

		sort.Sort(entries)

		if h.maxLen < int64(len(entries)) {
			h.maxLen = int64(len(entries))
		}
		total += int64(len(entries))
	}

	if len(h.builder.values) > 0 {
		h.totalTokens = len(h.builder.values)
		h.avgLen = total / int64(h.totalTokens)

		err := h.machine.Build(keys)
		util.PanicIfErr(err, "build ac machine fail")
	}
}
