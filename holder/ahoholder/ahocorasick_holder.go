package ahoholder

import (
	"fmt"
	"sort"
	"strings"

	aho "github.com/anknown/ahocorasick"
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/codegen/cache"
	indexstore "github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	ACHolderOption struct {
		// QuerySep 查询时，当存在多个值时，使用什么分隔符拼接多个查询字段来组成查询语句, 默认使用whitespace
		// 这是因为在语义上'空'更符合逻辑表达的正确性, 但这也会导致输入的句子如果本身有空格的情况，可能导致拼接之后误匹配问题
		QuerySep string
	}

	ACBuilder struct {
		ACHolderOption
		values map[string]core.Entries
	}

	ACIndex struct {
		ACHolderOption
		totalTokens int
		maxLen      int64 // max length of Entries
		avgLen      int64 // avg length of Entries

		values  map[string]core.Entries
		machine *aho.Machine // matcher     *cedar.Matcher
	}

	AcHolderTxData struct {
		Keys cache.StrListValues
	}
)

func init() {
	be_indexer.RegisterField(core.HolderNameACMatcher, core.FieldImplementation{
		NewBuilder: func() core.FieldIndexBuilder { return NewACBuilder(ACHolderOption{QuerySep: " "}) },
		NewIndex:   func() core.FieldIndex { return NewACIndex(ACHolderOption{QuerySep: " "}) },
	})
}

func NewACBuilder(option ACHolderOption) *ACBuilder {
	return &ACBuilder{
		ACHolderOption: option,
		values:         map[string]core.Entries{},
	}
}

func NewACIndex(option ACHolderOption) *ACIndex {
	return &ACIndex{
		ACHolderOption: option,
		values:         map[string]core.Entries{},
		machine:        new(aho.Machine),
	}
}

func (txd *AcHolderTxData) Encode() ([]byte, error) {
	return proto.Marshal(&txd.Keys)
}

func (h *ACBuilder) DecodeFieldIndexingData(data []byte) (core.IndexingData, error) {
	txData := &AcHolderTxData{
		Keys: cache.StrListValues{},
	}
	if len(data) == 0 {
		return txData, nil
	}
	err := proto.Unmarshal(data, &txData.Keys)
	return txData, err
}

func (h *ACBuilder) BuildFieldIndexingData(_ *core.FieldDesc, bv *core.ValueExpr) (core.IndexingData, error) {
	util.PanicIf(bv.Operator != core.ValueOptEQ, "ac_matcher container support EQ operator only")

	keys, err := ParseAcMatchDict(bv.Value)
	if err != nil {
		return nil, fmt.Errorf("ac holder need string(able) value, err:%v", err)
	}
	data := cache.StrListValues{
		Values: keys,
	}
	return &AcHolderTxData{Keys: data}, nil
}

func (h *ACBuilder) CommitFieldIndexingData(tx core.FieldIndexingData) error {
	if tx.Data == nil {
		return nil
	}
	var ok bool
	var data *AcHolderTxData
	if data, ok = tx.Data.(*AcHolderTxData); !ok {
		return fmt.Errorf("invalid Tx.Data type")
	}
	for _, v := range data.Keys.GetValues() {
		h.values[v] = append(h.values[v], tx.EID)
	}
	return nil
}

func (h *ACBuilder) CompileEntries() (core.FieldIndex, error) {
	holder := &ACIndex{
		ACHolderOption: h.ACHolderOption,
		values:         h.values,
		machine:        new(aho.Machine),
	}
	if err := holder.buildMachine(); err != nil {
		return nil, err
	}
	// Reset builder
	h.values = nil
	return holder, nil
}

// ------------------------------------------------------------------------------------------------
// ACIndex Implementation
// ------------------------------------------------------------------------------------------------

// DumpInfo
// {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (h *ACIndex) DumpInfo(buffer *strings.Builder) {
	info := fmt.Sprintf("{name: %s, value_count:%d max_entries:%d avg_entries:%d}",
		"ac_holder", len(h.values), h.maxLen, h.avgLen)
	buffer.WriteString(info)
}

func (h *ACIndex) GetEntries(field *core.FieldDesc, assigns core.Values) ([]core.PostingIterator, error) {
	if len(h.values) == 0 {
		return nil, nil
	}
	buf, err := BuildAcMatchContent(assigns, h.QuerySep)
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, nil
	}

	var cursors []core.PostingIterator

	terms := h.machine.MultiPatternSearch(buf, false)
	for _, term := range terms {
		key := string(term.Word)
		if pl, ok := h.values[key]; ok && len(pl) > 0 {
			cursor := be_indexer.NewSliceIterator(be_indexer.NewTerm(field.Field, key), pl)
			cursors = append(cursors, cursor)
		}
	}
	return cursors, nil
}

func (h *ACIndex) buildMachine() error {
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

func (h *ACIndex) Serialize() ([]byte, error) {
	dump := &indexstore.ACHolderDump{}
	dump.Entries = make([]*indexstore.ACEntry, 0, len(h.values))
	for keyword, entries := range h.values {
		ids := make([]uint64, len(entries))
		for i, id := range entries {
			ids[i] = uint64(id)
		}
		entry := &indexstore.ACEntry{
			Keyword: keyword,
			Ids:     &indexstore.EntryIDList{Ids: ids},
		}
		dump.Entries = append(dump.Entries, entry)
	}
	return proto.Marshal(dump)
}

func (h *ACIndex) Deserialize(data []byte) error {
	dump := &indexstore.ACHolderDump{}
	if err := proto.Unmarshal(data, dump); err != nil {
		return err
	}
	h.values = make(map[string]core.Entries)
	for _, entry := range dump.Entries {
		ids := make([]core.EntryID, len(entry.Ids.Ids))
		for i, v := range entry.Ids.Ids {
			ids[i] = core.EntryID(v)
		}
		h.values[entry.Keyword] = core.Entries(ids)
	}
	// Rebuild AC machine
	return h.buildMachine()
}

