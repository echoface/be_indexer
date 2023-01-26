package rangeholder

import (
	"container/list"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	. "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

type (
	// ExtendLgtHolder implement base on default holder extend support for LT/GT operator
	ExtendLgtHolder struct {
		RangeHolderOption

		debug  bool
		maxLen int // max length of Entries
		avgLen int // avg length of Entries

		rangeIdx  *RangeIdx         // range expression container
		plEntries map[int64]Entries // in/not in value expression container
	}

	RangeHolderOption struct {
		EnableFloat2Int    bool
		RangeCvtValuesSize float64
		RangeMax           int64
		RangeMin           int64
	}
	RangeOptionFn func(option *RangeHolderOption)

	LtGtTxData struct {
		Operator ValueOpt `json:"operator"`
		RgValue  *Range   `json:"range,omitempty"`
		EqValues []int64  `json:"eq_values,omitempty"`
	}
)

func init() {
	RegisterEntriesHolder(HolderNameExtendRange, func() EntriesHolder {
		defaultHolderOption := NewRangeHolderOption()
		return NewNumberExtendRangeHolder(WithRangeHolderOption(defaultHolderOption))
	})
}

func NewRangeHolderOption() *RangeHolderOption {
	return &RangeHolderOption{
		EnableFloat2Int:    true,
		RangeCvtValuesSize: 256,
		RangeMax:           math.MaxInt64,
		RangeMin:           math.MinInt64,
	}
}

func WithRangeHolderOption(opt *RangeHolderOption) RangeOptionFn {
	return func(option *RangeHolderOption) {
		*option = *opt
	}
}

func NewNumberExtendRangeHolder(fns ...RangeOptionFn) *ExtendLgtHolder {
	option := NewRangeHolderOption()
	for _, fn := range fns {
		fn(option)
	}
	rangeIdx := NewRangeIdx(option.RangeMin, option.RangeMax)
	holder := &ExtendLgtHolder{
		plEntries:         map[int64]Entries{},
		rangeIdx:          rangeIdx,
		RangeHolderOption: *option,
	}
	return holder
}

func (txd *LtGtTxData) BetterToCache() bool {
	return len(txd.EqValues) > BetterToCacheMaxItemsCount
}

func (txd *LtGtTxData) Encode() ([]byte, error) {
	return json.Marshal(txd)
}

func (h *ExtendLgtHolder) DecodeTxData(data []byte) (TxData, error) {
	var txd LtGtTxData
	err := json.Unmarshal(data, &txd)
	return &txd, err
}

func (h *ExtendLgtHolder) EnableDebug(debug bool) {
	h.debug = debug
}

func (h *ExtendLgtHolder) DumpInfo(buffer *strings.Builder) {
	summarys := map[string]interface{}{
		"name":               "ExtendLgtHolder",
		"kvCnt":              len(h.plEntries),
		"maxEntriesLen":      h.maxLen,
		"avgEntriesLen":      h.avgLen,
		"RangeMaxEntriesLen": h.rangeIdx.maxLen,
		"RangeAvgEntriesLen": h.rangeIdx.avgLen,
		"EnableFloat2Int":    h.EnableFloat2Int,
		"RangeCvtValuesSize": h.RangeCvtValuesSize,
	}
	buffer.WriteString(util.JSONPretty(summarys))
}

func (h *ExtendLgtHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("ExtendLgtEntriesHolder entries:\n>>kv entries")
	for v, entries := range h.plEntries {
		buffer.WriteString(fmt.Sprintf("\n%d:", v))
		buffer.WriteString(strings.Join(entries.DocString(), ","))
	}
	buffer.WriteString("\n>>range entries")
	buffer.WriteString(h.rangeIdx.String())
}

func (h *ExtendLgtHolder) CompileEntries() error {
	var total int
	valueCnt := len(h.plEntries)

	for _, entries := range h.plEntries {
		sort.Sort(entries)

		if h.maxLen < len(entries) {
			h.maxLen = len(entries)
		}
		total += len(entries)
	}
	if valueCnt > 0 {
		h.avgLen = total / valueCnt
	}

	h.rangeIdx.Compile()
	return nil
}

func (h *ExtendLgtHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
	var ids []int64
	if ids, e = parser.ParseIntergers(assigns, h.EnableFloat2Int); e != nil {
		return nil, e
	}
	if len(ids) <= 0 {
		return r, nil
	}
	rangeResults := map[*RangeEntries]int64{}
	for _, id := range ids {
		if entries, hit := h.plEntries[id]; hit {
			r = append(r, NewEntriesCursor(NewQKey(field.Field, id), entries))
			LogInfoIf(h.debug, "kvs find:<%s:%d>, entries len:%d", field.Field, id, len(entries))
		}
		if pl := h.rangeIdx.Retrieve(id); pl != nil && len(pl.entries) > 0 {
			rangeResults[pl] = id
			LogInfoIf(h.debug, "range find:<%s:%d>, entries len:%d", field.Field, id, len(pl.entries))
		}
	}
	for rgPl, id := range rangeResults {
		r = append(r, NewEntriesCursor(NewQKey(field.Field, id), rgPl.entries))
	}
	return r, nil
}

func ParseBetween(value Values) (*Range, error) {
	var left, right int64
	switch v := value.(type) {
	case [2]int64:
		left, right = v[0], v[1]
	case []int64:
		if len(v) != 2 {
			return nil, fmt.Errorf("operator Between need two, input lenght:%d", len(v))
		}
		left, right = v[0], v[1]
	case string:
		if rgDesc := parser.NewRangeDesc(v); rgDesc != nil {
			left, right, _ = rgDesc.Values()
		} else {
			return nil, fmt.Errorf("not a valid range description, need x:y input:%s", v)
		}
	}
	if left > right {
		return nil, fmt.Errorf("%d > %d, bad range", left, right)
	}
	return NewRange(left, right), nil
}

func ParseRange(opt ValueOpt, value Values, enableF2I bool) (*Range, error) {
	switch opt {
	case ValueOptBetween:
		return ParseBetween(value)
	case ValueOptGT, ValueOptLT:
		var number int64
		var err error
		if number, err = parser.ParseIntegerNumber(value, enableF2I); err != nil {
			return nil, fmt.Errorf("lt/gt operator need interger, parse:%v err:%v", value, err)
		}
		if opt == ValueOptLT {
			return NewRange(math.MinInt64, number), nil
		}
		return NewRange(number+1, math.MaxInt64), nil
	default:
		break
	}
	return nil, fmt.Errorf("not supported operator:%d", opt)
}

func (h *ExtendLgtHolder) IndexingBETx(field *FieldDesc, values *BoolValues) (r TxData, e error) {
	switch values.Operator {
	case ValueOptEQ: // NOTE: ids can be replicated if expression contain cross condition
		var ids []int64
		if ids, e = parser.ParseIntergers(values.Value, h.EnableFloat2Int); e != nil {
			return r, fmt.Errorf("field:%s value:%+v parse fail, err:%v", field.Field, values, e)
		}
		return &LtGtTxData{EqValues: ids, Operator: ValueOptEQ}, nil
	case ValueOptLT, ValueOptGT, ValueOptBetween:
		rg, err := ParseRange(values.Operator, values.Value, h.EnableFloat2Int)
		if err != nil {
			return r, err
		}
		txData := &LtGtTxData{Operator: ValueOptBetween, RgValue: rg}
		if rg.Size() < h.RangeCvtValuesSize {
			txData.Operator, txData.RgValue = ValueOptEQ, nil
			txData.EqValues = make([]int64, 0, int(rg.Size()))
			for i := rg.left; i < rg.right; i++ {
				txData.EqValues = append(txData.EqValues, i)
			}
		}
		return txData, nil
	default:
		break
	}
	return nil, fmt.Errorf("unsupport Operator:%d", values.Operator)
}

func (h *ExtendLgtHolder) CommitIndexingBETx(tx IndexingBETx) error {
	if tx.Data == nil {
		return nil
	}
	data := tx.Data.(*LtGtTxData)
	switch data.Operator {
	case ValueOptEQ: // NOTE: ids can be replicated if expression contain cross condition
		values := util.DistinctInteger(data.EqValues)
		for _, id := range values {
			h.plEntries[id] = append(h.plEntries[id], tx.EID)
		}
	case ValueOptGT, ValueOptLT, ValueOptBetween:
		h.rangeIdx.IndexingRange(data.RgValue.left, data.RgValue.right, tx.EID)
	default:
		return fmt.Errorf("what happened")
	}
	return nil
}

type (
	Range struct {
		left  int64
		right int64
	}

	RangeEntries struct { // range -> postinglist
		Range

		entries Entries
	}

	RangePlList []*RangeEntries

	// RangeIdx init status: [-inf, inf]
	// after indexing, only need rgEntries, items will be reset
	RangeIdx struct {
		_compiled bool
		maxLen    int // max length of Entries
		avgLen    int // avg length of Entries
		valueMin  int64
		valueMax  int64

		items     *list.List  // lifetime: builder stage
		rgEntries RangePlList // lifetime: indexing data for retrieve
	}
)

func NewRangeEntries(l, r int64) *RangeEntries {
	return &RangeEntries{Range: Range{left: l, right: r}}
}

func (rg *Range) String() string {
	if rg.IsLeftInf() && rg.IsRightInf() {
		return "(-inf,+inf)"
	} else if rg.IsRightInf() {
		return fmt.Sprintf("[%d,+inf)", rg.left)
	} else if rg.IsLeftInf() {
		return fmt.Sprintf("[-inf,%d)", rg.right)
	}
	return fmt.Sprintf("[%d,%d)", rg.left, rg.right)
}

func NewRange(l, r int64) *Range {
	if l == r {
		r++
	}
	return &Range{l, r}
}

func (rg *Range) Size() float64 {
	return float64(rg.right) - float64(rg.left)
}

func (rg *Range) IsLeftInf() bool {
	return rg.left == math.MinInt64
}

func (rg *Range) IsRightInf() bool {
	return rg.left == math.MaxInt64
}

func (rg *Range) ContainValue(v int64) bool {
	return v >= rg.left && v < rg.right
}

func (rg *Range) Equal(other Range) bool {
	return rg.left == other.left && rg.right == other.right
}

func (rg *Range) ContainRange(other *Range) bool {
	return other.left >= rg.left &&
		other.right <= rg.right &&
		other.left < rg.right
}

func (rg *Range) Explode(left, right int64) (rgs []*Range) {
	util.PanicIf(!rg.ContainRange(&Range{left, right}), "bad sub range[%d,%d)", left, right)

	// 0, x, y, 100
	vs := []int64{rg.left}
	if left > rg.left {
		vs = append(vs, left)
	}
	vs = append(vs, right)
	if right < rg.right {
		vs = append(vs, rg.right)
	}

	leftValue := vs[0]
	for i := 1; i < len(vs) && leftValue < rg.right; i++ {
		if vs[i] == vs[i-1] {
			vs[i]++
		}
		rgs = append(rgs, &Range{leftValue, vs[i]})
		leftValue = vs[i]
	}
	// fmt.Printf("%v explode:%d,%d range vs:%v\n", rg, left, right, vs)
	return
}

func (re *RangeEntries) AppendEntry(eid EntryID) {
	re.entries = append(re.entries, eid)
}

func (re *RangeEntries) Clone() (v *RangeEntries) {
	v = NewRangeEntries(re.left, re.right)
	v.entries = make([]EntryID, len(re.entries))
	copy(v.entries, re.entries)
	return v
}

func NewRangeIdx(min, max int64) *RangeIdx {
	util.PanicIf(min >= max, "bad range:[%d,%d)", min, max)
	rgIdx := &RangeIdx{
		items:    list.New(),
		valueMax: max,
		valueMin: min,
	}
	pl := NewRangeEntries(min, max)
	rgIdx.items.PushBack(pl)
	return rgIdx
}

func (rix *RangeIdx) Compile() {
	rix.rgEntries = make([]*RangeEntries, 0, rix.items.Len())

	sumCnt := 0
	iter := rix.items.Front()
	for ; iter != nil; iter = iter.Next() {
		pl := iter.Value.(*RangeEntries)

		if cnt := pl.entries.Len(); cnt > 0 {

			sumCnt += cnt
			if rix.maxLen < cnt {
				rix.maxLen = cnt
			}

			sort.Sort(pl.entries)
		}
		rix.rgEntries = append(rix.rgEntries, pl)
	}

	rix.items = list.New()
	rix._compiled = true
	if len(rix.rgEntries) > 0 {
		rix.avgLen = sumCnt / len(rix.rgEntries)
	}
}

func (rix *RangeIdx) String() string {
	sb := strings.Builder{}
	iter := rix.items.Front()
	for ; iter != nil; iter = iter.Next() {
		pl := iter.Value.(*RangeEntries)
		docs := pl.entries.DocString()
		sb.WriteString(fmt.Sprintf("%s:pl:%v\n", pl.String(), strings.Join(docs, ",")))
	}
	return sb.String()
}

func (rix *RangeIdx) Retrieve(value int64) *RangeEntries {
	if value < rix.valueMin || value >= rix.valueMax {
		return nil
	}
	index, found := sort.Find(len(rix.rgEntries), func(i int) int {
		rg := rix.rgEntries[i]
		if rg.ContainValue(value) {
			return 0
		}
		if value >= rg.right {
			return 1
		}
		return -1
	})

	if !found {
		return nil
	}
	return rix.rgEntries[index]
}

func (rix *RangeIdx) scale(left, right int64) (l, r int64) {
	return util.MaxInt64(left, rix.valueMin), util.MinInt64(right, rix.valueMax)
}

func (rix *RangeIdx) IndexingRange(left, right int64, eid EntryID) {
	if left == right { // ensure Range's requirement
		right++
	}

	rg := NewRange(left, right)
	appendEIDPls := make([]*RangeEntries, 0, 3)

	iter := rix.items.Front()
	for ; iter != nil; iter = iter.Next() {
		pl := iter.Value.(*RangeEntries)
		// pl:         [ .... )
		// rg:  [ .... )
		if pl.left >= rg.right {
			break
		}
		// pl: [ .... ) ->
		// rg:        [ .... )
		if rg.left >= pl.right {
			continue
		}
		// return v >= rg.left && v < rg.right
		// pl.left < rg.right
		// 		pl.right > rg.left
		// case 1: pl:[ ... ) ...
		//         rg:   [ ......)
		// case 2: pl: [ ........... )
		//         rg:    [ ..... )

		if pl.ContainValue(rg.left) {
			plRange := pl.Range // copy it, pl will be modified bellow

			var rgs []*Range
			if rg.right > pl.right { // case 1
				rgs = pl.Explode(rg.left, pl.right)
				rg = NewRange(plRange.right, right)
			} else { // case 2: rg.right <= pl.right
				rgs = pl.Explode(rg.left, rg.right)
			}

			fullRg := NewRange(left, right)
			for i, item := range rgs {
				var node *RangeEntries
				if i == 0 { // modify current node range， don't need insert new node
					node = pl
					pl.Range = *item
				} else {
					node = pl.Clone()
					node.Range = *item
					iter = rix.items.InsertAfter(node, iter)
				}
				if fullRg.ContainRange(item) {
					appendEIDPls = append(appendEIDPls, node)
				}
			}
		}
	}

	for _, pl := range appendEIDPls {
		pl.AppendEntry(eid)
	}
}
