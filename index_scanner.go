package be_indexer

import (
	"fmt"
	"reflect"
	"strings"
)

/*
IndexScan
a scanner for indexer, it helps to retrieve result document id from posting entries
currently, it is used by BEIndex, but as a part of design, it should top on BEIndex
that seems more reasonable. so may be next version, should be refactored(fixed).
*/

const (
	LinearSkipDistance = 8
)

type (
	QKey struct {
		field BEField
		value interface{}
	}

	// EntriesCursor represent a posting list for one Assign
	// (age, 15): [1, 2, 5, 19, 22]
	// cursor:           ^
	EntriesCursor struct {
		key     QKey
		cursor  int // current cur cursor
		entries Entries

		idSize int
		curEID EntryID // 为了加速计算
	}
	EntriesCursors []EntriesCursor

	// FieldCursor for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldCursor struct {
		current     *EntriesCursor
		cursorGroup EntriesCursors
	}
	FieldCursors []FieldCursor
)

func NewQKey(field BEField, v interface{}) QKey {
	key := QKey{field: field, value: v}
	return key
}

func (key *QKey) String() string {
	switch v := key.value.(type) {
	case string:
		return fmt.Sprintf("[%s,%s]", key.field, v)
	case int8, int16, int, int32, int64, uint8, uint16, uint, uint32, uint64:
		return fmt.Sprintf("[%s,%d]", key.field, v)
	default:
		fmt.Println("unknown type", reflect.TypeOf(key.value).String())
	}
	return fmt.Sprintf("[%s,%+v]", key.field, key.value)
}

func NewEntriesCursor(key QKey, entries Entries) EntriesCursor {
	eid := NULLENTRY
	if len(entries) > 0 {
		eid = entries[0]
	}
	return EntriesCursor{
		key:     key,
		cursor:  0,
		curEID:  eid,
		entries: entries,
		idSize:  len(entries),
	}
}

func (ec *EntriesCursor) GetCurEntryID() EntryID {
	return ec.curEID
}

func (ec *EntriesCursor) linearSkipTo(id EntryID) EntryID {
	for ec.cursor < ec.idSize && ec.entries[ec.cursor] < id {
		ec.cursor++
	}
	if ec.cursor >= ec.idSize {
		ec.curEID = NULLENTRY
	} else {
		ec.curEID = ec.entries[ec.cursor]
	}
	return ec.curEID
}

func (ec *EntriesCursor) SkipTo(id EntryID) EntryID {
	if ec.curEID >= id {
		return ec.curEID
	}

	oc := ec.cursor

	bound := 1
	rightSideIndex := oc + bound
	for rightSideIndex < ec.idSize && ec.entries[rightSideIndex] < id { // notice: can't use <=
		ec.cursor = rightSideIndex
		bound = bound << 1
		rightSideIndex = oc + bound
	}
	if rightSideIndex > ec.idSize {
		rightSideIndex = ec.idSize
	}
	// id in the range: [ec.cursor, rightSideIndex)
	// reuse `bound` as `mid` value
	// fmt.Printf("cur:%d idx-range:[%d,%d) values:%v", oc, ec.cursor, rightSideIndex, ec.entries[ec.cursor:rightSideIndex])
	for ec.cursor < rightSideIndex && ec.entries[ec.cursor] < id {
		// if rightSideIndex-ec.cursor < LinearSkipDistance {
		// return ec.linearSkipTo(id)
		//}
		bound = (ec.cursor + rightSideIndex) >> 1
		if ec.entries[bound] >= id {
			rightSideIndex = bound
		} else {
			ec.cursor = bound + 1
		}
	}
	if ec.cursor >= ec.idSize {
		ec.curEID = NULLENTRY
	} else {
		ec.curEID = ec.entries[ec.cursor]
	}
	return ec.curEID
}

// Len FieldCursors sort API
func (s FieldCursors) Len() int      { return len(s) }
func (s FieldCursors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FieldCursors) Less(i, j int) bool {
	return s[i].GetCurEntryID() < s[j].GetCurEntryID()
}

// Sort golang's internal sort.Sort method have obvious overhead in performance.(runtime convTSlice)
// so here use a simple insert sort replace it. bz not much Element, may another quickSort here later
func (s FieldCursors) Sort() {
	x := len(s)
	if x <= 1 {
		return
	}
	// Do ShellSort pass with gap 6
	// It could be written in this simplified form cause b-a <= 12
	//if x <= 12 { // make it seems sorted
	//	for i := 6; i < x; i++ {
	//		if s[i].GetCurEntryID() < s[i-6].GetCurEntryID() {
	//			s[i], s[i-6] = s[i-6], s[i]
	//		}
	//	}
	//}
	for i := 1; i < x; i++ {
		for j := i; j > 0 && s[j].GetCurEntryID() < s[j-1].GetCurEntryID(); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (s FieldCursors) Dump() string {
	sb := &strings.Builder{}
	for _, fc := range s {
		fc.DumpEntries(sb)
	}
	return sb.String()
}

func (s FieldCursors) DumpJustCursors() string {
	sb := &strings.Builder{}
	for idx, fc := range s {
		if idx > 0 {
			sb.WriteString("\n")
		}
		fc.DumpCursorEntryID(sb)
	}
	return sb.String()
}

func NewFieldCursor(cursors ...EntriesCursor) FieldCursor {
	scanner := FieldCursor{
		current:     nil,
		cursorGroup: cursors,
	}
	for idx := range scanner.cursorGroup {
		cursor := scanner.cursorGroup[idx]
		if scanner.current == nil || cursor.curEID < scanner.current.curEID {
			scanner.current = &cursor
		}
	}
	return scanner
}

func (fc *FieldCursor) ReachEnd() bool {
	return fc.current.curEID.IsNULLEntry()
}

func (fc *FieldCursor) GetCurEntryID() EntryID {
	return fc.current.curEID
}

func (fc *FieldCursor) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for idx := range fc.cursorGroup {
		cur := &fc.cursorGroup[idx]
		if eid := cur.SkipTo(id); eid <= newMin {
			newMin = eid
			fc.current = cur
		}
	}
	return
}

func (fc *FieldCursor) DumpEntries(sb *strings.Builder) {
	sb.WriteString("============== Field:%s Cursors ==============\n")
	for idx := range fc.cursorGroup {
		it := &fc.cursorGroup[idx]
		if it == fc.current {
			sb.WriteString(">")
		} else {
			sb.WriteString(" ")
		}
		it.DumpEntries(sb)
		sb.WriteString("\n")
	}
}

func (fc *FieldCursor) DumpCursorEntryID(sb *strings.Builder) {
	sb.WriteString("============== Field Cursor EID ==============\n")
	for idx := range fc.cursorGroup {
		cursor := &fc.cursorGroup[idx]
		if cursor == fc.current {
			sb.WriteString(">")
		} else {
			sb.WriteString(" ")
		}
		sb.WriteString(cursor.key.String())
		curEIDStr := cursor.GetCurEntryID().DocString()
		sb.WriteString(fmt.Sprintf(",idx:%02d,EID:%s\n", cursor.cursor, curEIDStr))
	}
}

// DumpEntries in normal cases, posting-list has thousands/million ids,
// so here only dump part of (nearby) ids about current cursor
// [age,12]^<2,false>:<1,true>,<2,false><nil,nil>
func (ec *EntriesCursor) DumpEntries(sb *strings.Builder) {
	sb.WriteString(ec.key.String())
	sb.WriteString(fmt.Sprintf(",idx:%02d,EID:", ec.cursor))
	left := ec.cursor - 2
	if left < 0 {
		left = 0
	}
	right := ec.cursor + 10
	if right >= len(ec.entries) {
		right = len(ec.entries)
	}
	if left > 0 {
		sb.WriteString("...,")
	}
	// [left,right)
	for i := left; i < right; i++ {
		if i == ec.cursor {
			sb.WriteString("^")
		}
		sb.WriteString(ec.entries[i].DocString())
		if i != right-1 {
			sb.WriteString(",")
		}
	}
	if remain := len(ec.entries) - right; remain > 0 {
		sb.WriteString(fmt.Sprintf("...another %d", remain))
	}
}
