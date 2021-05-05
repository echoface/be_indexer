package be_indexer

import (
	"fmt"
	"strings"
)

const (
	NULLENTRY EntryID = 0xFFFFFFFFFFFFFFFF

	MaxBEFieldID uint64 = 0xFF             // 8bit
	MaxBEValueID uint64 = 0xFFFFFFFFFFFFFF // 56bit
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
	PostingList struct {
		key     Key
		cursor  int // current cur cursor
		entries Entries
	}

	PostingLists []*PostingList

	// for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldPostingListGroup struct {
		current *PostingList
		plGroup PostingLists
	}
	FieldPostingListGroups []*FieldPostingListGroup
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

func NewPostingList(key Key, entries Entries) *PostingList {
	return &PostingList{
		key:     key,
		cursor:  0,
		entries: entries,
	}
}

func (pl *PostingList) ReachEnd() bool {
	return pl.entries == nil || len(pl.entries) <= pl.cursor
}

func (pl *PostingList) GetCurEntryID() EntryID {
	if pl.ReachEnd() {
		return NULLENTRY
	}
	return pl.entries[pl.cursor]
}

func (pl *PostingList) Skip(id EntryID) EntryID {
	entry := pl.GetCurEntryID()
	if entry > id {
		return entry
	}

	leftIdx := pl.cursor
	rightIdx := len(pl.entries)
	var mid int
	for leftIdx < rightIdx {
		mid = (leftIdx + rightIdx) >> 1
		tId := pl.entries[mid]
		if tId <= id {
			leftIdx = mid + 1
		} else {
			rightIdx = mid
		}
		if leftIdx >= len(pl.entries) || pl.entries[leftIdx] > id {
			break
		}
	}
	pl.cursor = leftIdx
	return pl.GetCurEntryID()
}

func (pl *PostingList) SkipTo(id EntryID) EntryID {
	entry := pl.GetCurEntryID()
	if entry > id {
		return entry
	}
	leftIdx := pl.cursor
	rightIdx := len(pl.entries)
	var mid int
	for leftIdx < rightIdx {
		mid = (leftIdx + rightIdx) >> 1
		tId := pl.entries[mid]
		if tId >= id {
			rightIdx = mid
		} else {
			leftIdx = mid + 1
		}
		if leftIdx >= len(pl.entries) || pl.entries[leftIdx] >= id {
			break
		}
	}
	pl.cursor = leftIdx
	return pl.GetCurEntryID()
}

//FieldPostingGroups sort API
func (s FieldPostingListGroups) Len() int      { return len(s) }
func (s FieldPostingListGroups) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FieldPostingListGroups) Less(i, j int) bool {
	return s[i].GetCurEntryID() < s[j].GetCurEntryID()
}

func (s FieldPostingListGroups) Dump() string {
	sb := &strings.Builder{}
	sb.WriteString(fmt.Sprintf("total plgs:%d\n", len(s)))
	for idx, pl := range s {
		sb.WriteString(fmt.Sprintf("%d:", idx))
		sb.WriteString(fmt.Sprintf("%v\n", pl.DumpPostingList()))
	}
	return sb.String()
}

func NewFieldPostingListGroup(pls ...*PostingList) *FieldPostingListGroup {
	plg := &FieldPostingListGroup{
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

func (plg *FieldPostingListGroup) AddPostingList(pl *PostingList) {
	plg.plGroup = append(plg.plGroup, pl)
	if plg.current == nil {
		plg.current = pl
		return
	}
	if pl.GetCurEntryID() < plg.current.GetCurEntryID() {
		plg.current = pl
	}
}

func (plg *FieldPostingListGroup) GetCurConjID() ConjID {
	return plg.GetCurEntryID().GetConjID()
}

func (plg *FieldPostingListGroup) GetCurEntryID() EntryID {
	return plg.current.GetCurEntryID()
}

func (plg *FieldPostingListGroup) Skip(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, pl := range plg.plGroup {
		if tId := pl.Skip(id); tId < newMin {
			newMin = tId
			plg.current = pl
		}
	}
	return
}

func (plg *FieldPostingListGroup) SkipTo(id EntryID) (newMin EntryID) {
	newMin = NULLENTRY
	for _, pl := range plg.plGroup {
		if tId := pl.SkipTo(id); tId < newMin {
			newMin = tId
			plg.current = pl
		}
	}
	return
}

func (plg *FieldPostingListGroup) DumpPostingList() string {
	sb := &strings.Builder{}
	for idx, pl := range plg.plGroup {
		sb.WriteString(fmt.Sprintf("\n"))
		sb.WriteString(fmt.Sprintf("idx:%d#%d#cur:%v", idx, pl.key, pl.GetCurEntryID().DocString()))
		sb.WriteString(fmt.Sprintf(" entries:%v", pl.entries.DocString()))
	}
	return sb.String()
}
