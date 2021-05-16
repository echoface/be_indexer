package be_indexer

import (
	"fmt"
	"sort"
	"strings"
)

const (
	NULLENTRY EntryID = 0xFFFFFFFFFFFFFFFF

	MaxBEFieldID uint64 = 0xFF             // 8bit
	MaxBEValueID uint64 = 0xFFFFFFFFFFFFFF // 56bit

	LinearSearchLengthThreshold = 128
)

type (
	//Field-Value eg: age-15 all field mapping to int32
	// <field-8bit> | <value-56bit>
	Key uint64

	EntryID uint64
	Entries []EntryID

	/* represent a posting list for one Assign */
	// (age, 15): [1, 2, 5, 19, 22]
	// cursor:           ^
	EntriesScanner struct {
		key     Key
		cursor  int // current cur cursor
		entries Entries
	}

	EntriesScanners []*EntriesScanner

	// for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldScannerGroup struct {
		current *EntriesScanner
		plGroup EntriesScanners
	}
	FieldScannerGroups []*FieldScannerGroup

	// PostingEntries posting list entries(sorted); eg: <age, 15>: []EntryID{1, 2, 3}
	PostingEntries struct {
		maxLen    int64 // max length of Entries
		avgLen    int64 // avg length of Entries
		plEntries map[Key]Entries
	}
)

//Key API
func NewKey(fieldID uint64, valueID uint64) Key {
	if fieldID > MaxBEFieldID || valueID > MaxBEValueID {
		panic(fmt.Errorf("out of value range, <%d, %d>", fieldID, valueID))
	}
	return Key(fieldID<<56 | valueID)
}

func (key Key) GetFieldID() uint64 {
	return uint64(key >> 56 & 0xFF)
}

func (key Key) GetValueID() uint64 {
	return uint64(key) & MaxBEValueID
}

//Entries sort API
func (s Entries) Len() int           { return len(s) }
func (s Entries) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s Entries) Less(i, j int) bool { return s[i] < s[j] }

func (s Entries) DocString() []string {
	res := make([]string, 0, len(s))
	for _, eid := range s {
		res = append(res, eid.DocString())
	}
	return res
}

//NewEntryID |-- ConjID(48bit) --|-- empty(15bit) -- | --incl/excl(1bit) --|
func NewEntryID(id ConjID, incl bool) EntryID {
	if !incl {
		return EntryID(id << 16)
	}
	return EntryID((id << 16) | 0x01)
}

func (entry EntryID) IsExclude() bool {
	return entry&0x01 == 0
}

func (entry EntryID) IsInclude() bool {
	return entry&0x01 > 0
}

func (entry EntryID) GetConjID() ConjID {
	return ConjID(entry >> 16)
}

func (entry EntryID) IsNULLEntry() bool {
	return entry == NULLENTRY
}

func (entry EntryID) DocString() string {
	if entry.IsNULLEntry() {
		return "<nil,nil>"
	}
	return fmt.Sprintf("<%d,%t>", entry.GetConjID().DocID(), entry.IsInclude())
}

func NewPostingList(key Key, entries Entries) *EntriesScanner {
	return &EntriesScanner{
		key:     key,
		cursor:  0,
		entries: entries,
	}
}

func (sc *EntriesScanner) GetCurEntryID() EntryID {
	if len(sc.entries) <= sc.cursor {
		return NULLENTRY
	}
	return sc.entries[sc.cursor]
}

func (sc *EntriesScanner) LinearSkip(id EntryID) EntryID {
	entry := sc.GetCurEntryID()
	if entry > id {
		return entry
	}
	size := len(sc.entries)
	for ; sc.cursor < size && sc.entries[sc.cursor] <= id; sc.cursor++ {
	}
	return sc.GetCurEntryID()
}

func (sc *EntriesScanner) Skip(id EntryID) EntryID {

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

func (sc *EntriesScanner) LinearSkipTo(id EntryID) EntryID {
	entry := sc.GetCurEntryID()
	if entry >= id {
		return entry
	}
	size := len(sc.entries)
	for ; sc.cursor < size && sc.entries[sc.cursor] < id; sc.cursor++ {
	}
	return sc.GetCurEntryID()
}

func (sc *EntriesScanner) SkipTo(id EntryID) EntryID {
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

//FieldPostingGroups sort API
func (s FieldScannerGroups) Len() int      { return len(s) }
func (s FieldScannerGroups) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FieldScannerGroups) Less(i, j int) bool {
	return s[i].GetCurEntryID() < s[j].GetCurEntryID()
}

func SortPostingListGroups(plgs []*FieldScannerGroup) {
	x := len(plgs)
	if x <= 1 {
		return
	}
	// Do ShellSort pass with gap 6
	// It could be written in this simplified form cause b-a <= 12
	if x <= 12 { // make it seems sorted
		for i := 6; i < x; i++ {
			if plgs[i].GetCurEntryID() < plgs[i-6].GetCurEntryID() {
				plgs[i], plgs[i-6] = plgs[i-6], plgs[i]
			}
		}
	}
	for i := 1; i < x; i++ {
		for j := i; j > 0 && plgs[j].GetCurEntryID() < plgs[j-1].GetCurEntryID(); j-- {
			plgs[j], plgs[j-1] = plgs[j-1], plgs[j]
		}
	}
}

func (s FieldScannerGroups) Dump() string {
	sb := &strings.Builder{}
	sb.WriteString(fmt.Sprintf("total plgs:%d\n", len(s)))
	for idx, pl := range s {
		sb.WriteString(fmt.Sprintf("%d:", idx))
		sb.WriteString(fmt.Sprintf("%v\n", pl.DumpPostingList()))
	}
	return sb.String()
}

func NewFieldPostingListGroup(pls ...*EntriesScanner) *FieldScannerGroup {
	plg := &FieldScannerGroup{
		current: nil,
		plGroup: pls,
	}
	for _, pl := range plg.plGroup {
		if plg.current == nil ||
			pl.GetCurEntryID() < plg.current.GetCurEntryID() {

			plg.current = pl
		}
	}
	return plg
}

func (sg *FieldScannerGroup) AddPostingList(pl *EntriesScanner) {
	sg.plGroup = append(sg.plGroup, pl)
	if sg.current == nil {
		sg.current = pl
		return
	}
	if pl.GetCurEntryID() < sg.current.GetCurEntryID() {
		sg.current = pl
	}
}

func (sg *FieldScannerGroup) GetCurConjID() ConjID {
	return sg.GetCurEntryID().GetConjID()
}

func (sg *FieldScannerGroup) GetCurEntryID() EntryID {
	return sg.current.GetCurEntryID()
}

func (sg *FieldScannerGroup) Skip(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, pl := range sg.plGroup {
		if tId := pl.Skip(id); tId < newMin {
			newMin = tId
			sg.current = pl
		}
	}
	return
}

func (sg *FieldScannerGroup) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, pl := range sg.plGroup {
		if tId := pl.SkipTo(id); tId < newMin {
			newMin = tId
			sg.current = pl
		}
	}
	return
}

func (sg *FieldScannerGroup) DumpPostingList() string {
	sb := &strings.Builder{}
	for idx, pl := range sg.plGroup {
		sb.WriteString(fmt.Sprintf("\n"))
		sb.WriteString(fmt.Sprintf("idx:%d#%d#cur:%v", idx, pl.key, pl.GetCurEntryID().DocString()))
		sb.WriteString(fmt.Sprintf(" entries:%v", pl.entries.DocString()))
	}
	return sb.String()
}

func (kse *PostingEntries) AppendEntryID(key Key, id EntryID) {
	entries, hit := kse.plEntries[key]
	if !hit {
		kse.plEntries[key] = Entries{id}
	}
	entries = append(entries, id)
	kse.plEntries[key] = entries
}

func (kse *PostingEntries) getEntries(key Key) Entries {
	if entries, hit := kse.plEntries[key]; hit {
		return entries
	}
	return nil
}

func (kse *PostingEntries) makeEntriesSorted() {
	var total int64
	for _, entries := range kse.plEntries {
		sort.Sort(entries)
		if kse.maxLen < int64(len(entries)) {
			kse.maxLen = int64(len(entries))
		}
		total += int64(len(entries))
	}
	if len(kse.plEntries) > 0 {
		kse.avgLen = total / int64(len(kse.plEntries))
	}
}
