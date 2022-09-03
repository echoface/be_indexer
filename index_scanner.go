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
	FastTestOffset     = 256
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
		// Local slice: length will be cached and there is no overhead
		// Global slice or passed (by reference): length cannot be cached and there is overhead
		idSize int
	}
	EntriesCursors []*EntriesCursor

	// FieldCursor for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldCursor struct {
		current     *EntriesCursor
		cursorGroup EntriesCursors
	}
	FieldCursors []*FieldCursor
)

func newQKey(field BEField, v interface{}) QKey {
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

func NewEntriesCursor(key QKey, entries Entries) *EntriesCursor {
	return &EntriesCursor{
		key:     key,
		cursor:  0,
		entries: entries,
		idSize:  len(entries),
	}
}

func (ec *EntriesCursor) GetCurEntryID() EntryID {
	if ec.idSize > ec.cursor {
		return ec.entries[ec.cursor]
	}
	return NULLENTRY
}

func (ec *EntriesCursor) LinearSkip(id EntryID) EntryID {
	maxRight := len(ec.entries) - 1
	for ec.cursor <= maxRight && ec.entries[ec.cursor] <= id {
		ec.cursor++
	}
	return ec.GetCurEntryID()
}

// Skip https://en.m.wikipedia.org/wiki/Exponential_search
// most cases, the target id is far away of cursor position
func (ec *EntriesCursor) Skip(id EntryID) EntryID {
	if entry := ec.GetCurEntryID(); entry > id {
		return entry
	}
	rightIdx := ec.cursor + 1
	maxRight := len(ec.entries) - 1

	for rightIdx <= maxRight && ec.entries[rightIdx] <= id {
		ec.cursor = rightIdx
		rightIdx = (rightIdx << 1) // 溢出? 64bit machine
	}
	if rightIdx > maxRight {
		rightIdx = maxRight
	}
	if rightIdx-ec.cursor < LinearSkipDistance {
		return ec.LinearSkip(id)
	}
	var mid int
	for ec.cursor <= rightIdx && ec.entries[ec.cursor] <= id {
		mid = (ec.cursor + rightIdx) >> 1
		if ec.entries[mid] <= id {
			ec.cursor = mid + 1
		} else {
			rightIdx = mid
		}
	}
	return ec.GetCurEntryID()
}

func (ec *EntriesCursor) LinearSkipTo(id EntryID) EntryID {
	maxRight := len(ec.entries) - 1
	for ec.cursor <= maxRight && ec.entries[ec.cursor] < id {
		ec.cursor++
	}
	return ec.GetCurEntryID()
}

func (ec *EntriesCursor) SkipTo(id EntryID) EntryID {
	if entry := ec.GetCurEntryID(); entry >= id {
		return entry
	}
	rightIdx := ec.cursor + 1
	maxRight := len(ec.entries) - 1

	for rightIdx <= maxRight && ec.entries[rightIdx] < id {
		ec.cursor = rightIdx
		rightIdx = (rightIdx << 1) // 溢出? 64bit machine
	}
	if rightIdx > maxRight {
		rightIdx = maxRight
	}
	if rightIdx-ec.cursor < LinearSkipDistance {
		return ec.LinearSkipTo(id)
	}

	var mid int
	for ec.cursor <= rightIdx && ec.entries[ec.cursor] < id {
		if rightIdx-ec.cursor < LinearSkipDistance {
			return ec.LinearSkipTo(id)
		}

		mid = (ec.cursor + rightIdx) >> 1
		if ec.entries[mid] >= id {
			rightIdx = mid
		} else {
			ec.cursor = mid + 1
		}
	}
	return ec.GetCurEntryID()
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
	if x <= 12 { // make it seems sorted
		for i := 6; i < x; i++ {
			if s[i].GetCurEntryID() < s[i-6].GetCurEntryID() {
				s[i], s[i-6] = s[i-6], s[i]
			}
		}
	}
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
		sb.WriteString("\n")
	}
	return sb.String()
}

func NewFieldCursor(cursors ...*EntriesCursor) *FieldCursor {
	scanner := &FieldCursor{
		current:     nil,
		cursorGroup: cursors,
	}
	for _, pl := range scanner.cursorGroup {
		if scanner.current == nil ||
			pl.GetCurEntryID() < scanner.current.GetCurEntryID() {

			scanner.current = pl
		}
	}
	return scanner
}

func (fc *FieldCursor) AddPostingList(cursor *EntriesCursor) {
	fc.cursorGroup = append(fc.cursorGroup, cursor)
	if fc.current == nil {
		fc.current = cursor
		return
	}
	if cursor.GetCurEntryID() < fc.current.GetCurEntryID() {
		fc.current = cursor
	}
}

func (fc *FieldCursor) GetCurConjID() ConjID {
	return fc.GetCurEntryID().GetConjID()
}

func (fc *FieldCursor) ReachEnd() bool {
	return fc.current.GetCurEntryID().IsNULLEntry()
}

func (fc *FieldCursor) GetCurEntryID() EntryID {
	return fc.current.GetCurEntryID()
}

func (fc *FieldCursor) Skip(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range fc.cursorGroup {
		if tId := cursor.Skip(id); tId < newMin {
			newMin = tId
			fc.current = cursor
		}
	}
	return
}

func (fc *FieldCursor) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range fc.cursorGroup {
		if tId := cursor.SkipTo(id); tId < newMin {
			newMin = tId
			fc.current = cursor
		}
	}
	return
}

func (fc *FieldCursor) DumpEntries(sb *strings.Builder) {
	sb.WriteString("============== Field Cursors ==============\n")
	for _, it := range fc.cursorGroup {
		if it == fc.current {
			sb.WriteString(">")
		} else {
			sb.WriteString(" ")
		}
		it.DumpEntries(sb)
		sb.WriteString("\n")
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
