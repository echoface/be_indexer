package be_indexer

import (
	"fmt"
	"sort"
)

const (
	NULLENTRY EntryID = 0xFFFFFFFFFFFFFFFF

	MaxBEFieldID uint64 = 0xFF             // 8bit
	MaxBEValueID uint64 = 0xFFFFFFFFFFFFFF // 56bit

)

type (
	//Key Field-Value eg: age-15 all field mapping to int32
	// <field-8bit> | <value-56bit>
	Key uint64

	EntryID uint64
	Entries []EntryID

	// PostingEntries posting list entries(sorted); eg: <age, 15>: []EntryID{1, 2, 3}
	PostingEntries struct {
		maxLen    int64 // max length of Entries
		avgLen    int64 // avg length of Entries
		plEntries map[Key]Entries
	}
)

//NewKey API
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

//Len Entries sort API
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
