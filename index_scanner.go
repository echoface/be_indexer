package be_indexer

import (
	"fmt"
	"strings"
)

/*
IndexScanner
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

	/*EntriesCursor represent a posting list for one Assign */
	// (age, 15): [1, 2, 5, 19, 22]
	// cursor:           ^
	EntriesCursor struct {
		key     QKey
		cursor  int // current cur cursor
		entries Entries
	}
	CursorGroup []*EntriesCursor

	//FieldScanner for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldScanner struct {
		current     *EntriesCursor
		cursorGroup CursorGroup
	}
	FieldScanners []*FieldScanner
)

func newQKey(field BEField, v interface{}) QKey {
	return QKey{field: field, value: v}
}

func (key *QKey) String() string {
	return fmt.Sprintf("<%s,%+v>", key.field, key.value)
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

//Len FieldScanners sort API
func (s FieldScanners) Len() int      { return len(s) }
func (s FieldScanners) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FieldScanners) Less(i, j int) bool {
	return s[i].GetCurEntryID() < s[j].GetCurEntryID()
}

// Sort golang's internal sort.Sort method have obvious overhead in performance.(runtime convTSlice)
// so here use a simple insert sort replace it. bz not much Element, may another quickSort here later
func (s FieldScanners) Sort() {
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

func (s FieldScanners) Dump() string {
	sb := &strings.Builder{}
	sb.WriteString("\n")
	for idx, scanner := range s {
		sb.WriteString(fmt.Sprintf("\n%d,cur:%s:%s", idx, scanner.GetCurEntryID().DocString(), scanner.DumpEntries()))
	}
	sb.WriteString("\n")
	return sb.String()
}

func (s FieldScanners) DumpCurrent() string {
	sb := &strings.Builder{}
	for idx, pl := range s {
		sb.WriteString(fmt.Sprintf("idx:%d, eid:%s\n", idx, pl.GetCurEntryID().DocString()))
	}
	return sb.String()
}

func NewFieldScanner(cursors ...*EntriesCursor) *FieldScanner {
	scanner := &FieldScanner{
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

func (sg *FieldScanner) AddPostingList(cursor *EntriesCursor) {
	sg.cursorGroup = append(sg.cursorGroup, cursor)
	if sg.current == nil {
		sg.current = cursor
		return
	}
	if cursor.GetCurEntryID() < sg.current.GetCurEntryID() {
		sg.current = cursor
	}
}

func (sg *FieldScanner) GetCurConjID() ConjID {
	return sg.GetCurEntryID().GetConjID()
}

func (sg *FieldScanner) GetCurEntryID() EntryID {
	return sg.current.GetCurEntryID()
}

func (sg *FieldScanner) Skip(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range sg.cursorGroup {
		if tId := cursor.Skip(id); tId < newMin {
			newMin = tId
			sg.current = cursor
		}
	}
	return
}

func (sg *FieldScanner) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, cursor := range sg.cursorGroup {
		if tId := cursor.SkipTo(id); tId < newMin {
			newMin = tId
			sg.current = cursor
		}
	}
	return
}

func (sg *FieldScanner) DumpEntries() string {
	sb := &strings.Builder{}
	for idx, cursor := range sg.cursorGroup {
		sb.WriteString(fmt.Sprintf("\nidx:%d#%s#cur:%v", idx, cursor.key.String(), cursor.GetCurEntryID().DocString()))
		sb.WriteString(fmt.Sprintf(" entries:%v", cursor.entries.DocString()))
	}
	return sb.String()
}
