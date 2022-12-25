package be_indexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

type (
	// ExtendLgtHolder implement base on default holder extend support for LT/GT operator
	ExtendLgtHolder struct {
		debug      bool
		maxLen     int // max length of Entries
		avgLen     int // avg length of Entries
		ltValueCnt int
		gtValueCnt int

		// in/not in expression hash container
		plEntries map[int64]Entries

		// all "field > number" expression store here(sorted)
		// expr: A: age > 30
		// expr: B: age > 20
		// expr: C: age > 35
		// expr: D: age > 20
		// expr: E: age > 20
		// gtEntries: [20, 30, 35] // sorted
		//             |    |   |
		//			   |    |   [C]
		//             |    [A]
		//             |[B, D, E]
		gtEntries ValueEntries

		// all "field < number" expression store here(sorted)
		ltEntries ValueEntries

		_ltmap map[int64]*LgtEntries
		_gtmap map[int64]*LgtEntries

		EnableF2I bool
	}

	LgtEntries struct { // current default container can support max fields:256
		lgtValue int64
		entries  Entries
	}

	prepareData struct {
		operator ValueOpt
		lgtValue int64
		valueIDs []int64
	}

	ValueEntries []*LgtEntries
)

// Len Entries sort API
func (s ValueEntries) Len() int           { return len(s) }
func (s ValueEntries) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ValueEntries) Less(i, j int) bool { return s[i].lgtValue < s[j].lgtValue }

func NewDefaultExtLtGtHolder(f2i bool) *ExtendLgtHolder {
	return &ExtendLgtHolder{
		plEntries: map[int64]Entries{},
		_ltmap:    map[int64]*LgtEntries{},
		_gtmap:    map[int64]*LgtEntries{},
		EnableF2I: f2i,
	}
}

func (h *ExtendLgtHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo
func (h *ExtendLgtHolder) DumpInfo(buffer *strings.Builder) {
	infos := map[string]interface{}{
		"name":          "ExtendLgtHolder",
		"kvCnt":         len(h.plEntries),
		"ltCnt":         len(h.ltEntries),
		"gtCnt":         len(h.gtEntries),
		"maxEntriesLen": h.maxLen,
		"avgEntriesLen": h.avgLen,
		"EnableF2I":     h.EnableF2I,
	}
	buffer.WriteString(util.JSONPretty(infos))
}

func (h *ExtendLgtHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("ExtendLgtEntriesHolder entries:\n>>kv entries")
	for v, entries := range h.plEntries {
		buffer.WriteString(fmt.Sprintf("\n%d:", v))
		buffer.WriteString(strings.Join(entries.DocString(), ","))
	}
	buffer.WriteString("\n>>gt entries")
	for _, data := range h.gtEntries {
		buffer.WriteString(fmt.Sprintf("\n%d:", data.lgtValue))
		buffer.WriteString(strings.Join(data.entries.DocString(), ","))
	}
	buffer.WriteString("\n>>lt entries")
	for _, data := range h.ltEntries {
		buffer.WriteString(fmt.Sprintf("\n%d:", data.lgtValue))
		buffer.WriteString(strings.Join(data.entries.DocString(), ","))
	}
}

func (h *ExtendLgtHolder) CompileEntries() error {
	h.makeEntriesSorted()

	for v, lgtValueEntries := range h._gtmap {
		util.PanicIf(v != lgtValueEntries.lgtValue, "something going wroing")
		h.gtEntries = append(h.gtEntries, lgtValueEntries)
	}
	for v, lgtValueEntries := range h._ltmap {
		util.PanicIf(v != lgtValueEntries.lgtValue, "something going wroing")
		h.ltEntries = append(h.ltEntries, lgtValueEntries)
	}
	// no longer used after build, retrieve process use gtEntries/ltEntries
	h.ltValueCnt = len(h._ltmap)
	h.gtValueCnt = len(h._gtmap)
	h._ltmap, h._gtmap = nil, nil

	sort.Sort(h.ltEntries)
	sort.Sort(h.gtEntries)
	return nil
}

func (h *ExtendLgtHolder) GetEntries(field *FieldDesc, assigns Values) (r EntriesCursors, e error) {
	var ids []int64
	if ids, e = parser.ParseIntergers(assigns, h.EnableF2I); e != nil {
		return nil, e
	}
	if len(ids) <= 0 {
		return r, nil
	}
	minValudID, maxValudID := ids[0], ids[0]
	for _, id := range ids {
		minValudID = util.MinInt64(minValudID, id)
		maxValudID = util.MaxInt64(maxValudID, id)

		if entries := h.getEntries(id); len(entries) > 0 {
			r = append(r, NewEntriesCursor(newQKey(field.Field, id), entries))
			LogInfoIf(h.debug, "kvs find:<%s:%d>, entries len:%d", field.Field, id, len(entries))
		}
	}
	// [5, 15, 20, 21, 35, 40]
	//                     <-
	var data *LgtEntries
	for j := h.ltValueCnt - 1; j >= 0; j-- {
		data = h.ltEntries[j]
		if data.lgtValue > minValudID {
			key := newQKey(field.Field, data.lgtValue)
			r = append(r, NewEntriesCursor(key, data.entries))
			LogInfoIf(h.debug, "ltEntries find:<%s:%d,%d>, entries len:%d",
				field.Field, data.lgtValue, minValudID, len(data.entries))
			continue
		}
		break
	}
	// [5, 15, 20, 21, 35, 40]
	// ->
	for _, data = range h.gtEntries {
		if data.lgtValue < maxValudID {
			key := newQKey(field.Field, data.lgtValue)
			r = append(r, NewEntriesCursor(key, data.entries))
			LogInfoIf(h.debug, "gtEntries find:<%s:%d,%d>, entries len:%d",
				field.Field, data.lgtValue, maxValudID, (data.entries))
			continue
		}
		break
	}
	return r, nil
}

func (h *ExtendLgtHolder) PrepareAppend(field *FieldDesc, values *BoolValues) (r Preparation, e error) {
	// util.PanicIf(values.Operator != ValueOptEQ, "default container support EQ operator only")

	switch values.Operator {
	case ValueOptEQ: // NOTE: ids can be replicated if expression contain cross condition
		var ids []int64
		if ids, e = parser.ParseIntergers(values.Value, h.EnableF2I); e != nil {
			return r, fmt.Errorf("field:%s value:%+v parse fail, err:%v", field.Field, values, e)
		}
		r.Data = &prepareData{
			valueIDs: ids,
			operator: ValueOptEQ,
		}
	case ValueOptLT, ValueOptGT:
		var number int64
		if number, e = parser.ParseIntegerNumber(values.Value, h.EnableF2I); e != nil {
			return r, fmt.Errorf("lt/gt operator need interger, parse:%v err:%v", values.Value, e)
		}
		r.Data = &prepareData{
			lgtValue: number,
			operator: values.Operator,
		}
	default:
		return
	}
	return r, nil
}

// CommitAppend NOTE: if partial success for a conjunction will cause logic error
// so for a Holder implement should panic it if any errors happen
func (h *ExtendLgtHolder) CommitAppend(preparation *Preparation, eid EntryID) {
	if preparation.Data == nil {
		return
	}
	data := preparation.Data.(*prepareData)
	switch data.operator {
	case ValueOptEQ: // NOTE: ids can be replicated if expression contain cross condition
		values := util.DistinctInteger(data.valueIDs)
		for _, id := range values {
			h.plEntries[id] = append(h.plEntries[id], eid)
		}
	case ValueOptGT:
		var ok bool
		var valueEntries *LgtEntries
		if valueEntries, ok = h._gtmap[data.lgtValue]; !ok || valueEntries == nil {
			valueEntries = &LgtEntries{
				lgtValue: data.lgtValue,
				entries:  Entries{},
			}
			h._gtmap[data.lgtValue] = valueEntries
		}
		valueEntries.entries = append(valueEntries.entries, eid)
	case ValueOptLT:
		var ok bool
		var valueEntries *LgtEntries
		if valueEntries, ok = h._ltmap[data.lgtValue]; !ok || valueEntries == nil {
			valueEntries = &LgtEntries{
				lgtValue: data.lgtValue,
				entries:  Entries{},
			}
			h._ltmap[data.lgtValue] = valueEntries
		}
		valueEntries.entries = append(valueEntries.entries, eid)
	default:
		panic(fmt.Errorf("what happened?"))
	}
}

func (h *ExtendLgtHolder) getEntries(key int64) Entries {
	if entries, hit := h.plEntries[key]; hit {
		return entries
	}
	return nil
}

func (h *ExtendLgtHolder) makeEntriesSorted() {
	var total int
	var valueCnt int
	for _, entries := range h.plEntries {
		sort.Sort(entries)
		if h.maxLen < len(entries) {
			h.maxLen = len(entries)
		}
		total += len(entries)
	}
	for _, data := range h._gtmap {
		sort.Sort(data.entries)
		if h.maxLen < len(data.entries) {
			h.maxLen = len(data.entries)
		}
		total += len(data.entries)
	}
	for _, data := range h._ltmap {
		sort.Sort(data.entries)
		if h.maxLen < len(data.entries) {
			h.maxLen = len(data.entries)
		}
		total += len(data.entries)
	}
	valueCnt += len(h.plEntries)
	valueCnt += len(h.gtEntries)
	valueCnt += len(h.ltEntries)
	if valueCnt > 0 {
		h.avgLen = total / valueCnt
	}
}
