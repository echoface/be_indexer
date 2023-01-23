package be_indexer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	aho "github.com/anknown/ahocorasick"
	"github.com/echoface/be_indexer/codegen/cache"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
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

	AcHolderTxData cache.StrListValues
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

func (txd *AcHolderTxData) BetterToCache() bool {
	return len(txd.Values) > BetterToCacheMaxItemsCount
}

func (txd *AcHolderTxData) Encode() ([]byte, error) {
	return json.Marshal(txd.Values)
}

func (h *ACEntriesHolder) DecodeTxData(data []byte) (TxData, error) {
	if len(data) == 0 {
		return &AcHolderTxData{Values: nil}, nil
	}
	txData := &AcHolderTxData{Values: []string{}}
	err := proto.Unmarshal(data, (*cache.StrListValues)(txData))
	return txData, err
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

func (h *ACEntriesHolder) IndexingBETx(field *FieldDesc, bv *BoolValues) (TxData, error) {
	util.PanicIf(bv.Operator != ValueOptEQ, "ac_matcher container support EQ operator only")

	keys, err := util.ParseAcMatchDict(bv.Value)
	if err != nil {
		return nil, fmt.Errorf("ac holder need string(able) value, err:%v", err)
	}
	return &AcHolderTxData{Values: keys}, nil
}

func (h *ACEntriesHolder) CommitIndexingBETx(tx IndexingBETx) error {
	if tx.data == nil {
		return nil
	}
	var ok bool
	var data *AcHolderTxData
	if data, ok = tx.data.(*AcHolderTxData); !ok {
		return fmt.Errorf("invalid Tx.Data type")
	}
	for _, v := range data.Values {
		h.values[v] = append(h.values[v], tx.eid)
	}
	return nil
}

func (h *ACEntriesHolder) GetEntries(field *FieldDesc, assigns Values) (EntriesCursors, error) {
	if len(h.values) == 0 {
		return nil, nil
	}
	buf, err := util.BuildAcMatchContent(assigns, h.QuerySep)
	if err != nil {
		return nil, err
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
