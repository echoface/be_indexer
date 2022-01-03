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
	LinearSearchLengthThreshold = 8
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
	}
	CursorGroup []*EntriesCursor

	// FieldCursor for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldCursor struct {
		current     *EntriesCursor
		cursorGroup CursorGroup
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
	}
}

func (sc *EntriesCursor) GetCurEntryID() EntryID {
	if len(sc.entries) <= sc.cursor {
		return NULLENTRY
	}
	return sc.entries[sc.cursor]
}

func (sc *EntriesCursor) LinearSkip(id EntryID) EntryID {
	entry := sc.GetCurEntryID()
	if entry > id {
		return entry
	}
	size := len(sc.entries)
	for ; sc.cursor < size && sc.entries[sc.cursor] <= id; sc.cursor++ {
	}
	return sc.GetCurEntryID()
}

func (sc *EntriesCursor) Skip(id EntryID) EntryID {

	entry := sc.GetCurEntryID()
	if entry > id {
		return entry
	}

	//according generated asm code, for a reference slice, len() have overhead
	size := len(sc.entries)

	rightIdx := size
	var mid int
	for sc.cursor < rightIdx {
		if rightIdx-sc.cursor < LinearSearchLengthThreshold {
			return sc.LinearSkip(id)
		}

		mid = (sc.cursor + rightIdx) >> 1
		if sc.entries[mid] <= id {
			sc.cursor = mid + 1
		} else {
			rightIdx = mid
		}
		if sc.cursor >= size || sc.entries[sc.cursor] > id {
			break
		}
	}
	return sc.GetCurEntryID()
}

func (sc *EntriesCursor) LinearSkipTo(id EntryID) EntryID {
	entry := sc.GetCurEntryID()
	if entry >= id {
		return entry
	}
	size := len(sc.entries)
	for ; sc.cursor < size && sc.entries[sc.cursor] < id; sc.cursor++ {
	}
	return sc.GetCurEntryID()
}

func (sc *EntriesCursor) SkipTo(id EntryID) EntryID {
	entry := sc.GetCurEntryID()
	if entry >= id {
		return entry
	}

	//according generated asm code, for a reference slice, len() have overhead
	size := len(sc.entries)
	rightIdx := size

	var mid int
	for sc.cursor < rightIdx {
		if rightIdx-sc.cursor < LinearSearchLengthThreshold {
			return sc.LinearSkipTo(id)
		}
		mid = (sc.cursor + rightIdx) >> 1
		if sc.entries[mid] >= id {
			rightIdx = mid
		} else {
			sc.cursor = mid + 1
		}
		if sc.cursor >= size || sc.entries[sc.cursor] >= id {
			break
		}
	}
	return sc.GetCurEntryID()
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
	for _, scanner := range s {
		sb.WriteString(scanner.DumpEntries())
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s FieldCursors) DumpCurrent() string {
	sb := &strings.Builder{}
	for idx, pl := range s {
		sb.WriteString(fmt.Sprintf("idx:%d, eid:%s\n", idx, pl.GetCurEntryID().DocString()))
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

func (sg *FieldCursor) AddPostingList(cursor *EntriesCursor) {
	sg.cursorGroup = append(sg.cursorGroup, cursor)
	if sg.current == nil {
		sg.current = cursor
		return
	}
	if cursor.GetCurEntryID() < sg.current.GetCurEntryID() {
		sg.current = cursor
	}
}

func (sg *FieldCursor) GetCurConjID() ConjID {
	return sg.GetCurEntryID().GetConjID()
}

func (sg *FieldCursor) GetCurEntryID() EntryID {
	return sg.current.GetCurEntryID()
}

func (sg *FieldCursor) Skip(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range sg.cursorGroup {
		if tId := cursor.Skip(id); tId < newMin {
			newMin = tId
			sg.current = cursor
		}
	}
	return
}

func (sg *FieldCursor) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range sg.cursorGroup {
		if tId := cursor.SkipTo(id); tId < newMin {
			newMin = tId
			sg.current = cursor
		}
	}
	return
}

func (sg *FieldCursor) DumpEntries() string {
	sb := &strings.Builder{}
	sg.cursorGroup.DumpEntries(sb)
	return sb.String()
}

func (cur *EntriesCursor) DumpEntries(sb *strings.Builder) {
	// [xx,x]^<2,false>:<1,true>,<2,false><nil,nil>
	sb.WriteString(cur.key.String())
	sb.WriteString("^")
	sb.WriteString(cur.GetCurEntryID().DocString())
	sb.WriteString(":")
	sb.WriteString(strings.Join(cur.entries.DocString(), ","))
}

func (cg CursorGroup) DumpEntries(sb *strings.Builder) {
	sb.Grow(1024)
	for _, it := range cg {
		sb.WriteString("\t->")
		it.DumpEntries(sb)
		sb.WriteString("\n")
	}
}
