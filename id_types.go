package be_indexer

import (
	"fmt"
)

const (
	MaxDocID = 0x7FFFFFFFFFF

	NULLENTRY EntryID = 0xFFFFFFFFFFFFFFFF

	MaxBEFieldID uint64 = 0xFF             // 8bit
	MaxBEValueID uint64 = 0xFFFFFFFFFFFFFF // 56bit
)

type (
	// ConjID max support 56bit len
	// |--[(reserved(16)) | size(8bit) | index(8bit)  | docID(32bit)]
	ConjID uint64

	// Key is the term represent field and its value, eg: <age,15>
	// <field-8bit> | <value-56bit>
	Key uint64

	// EntryID [-- ConjID(48bit) --|-- empty(15bit) -- | --incl/excl(1bit) --]
	// |--[(reserved(16)) | size(8bit) | index(8bit)  | docID(32bit)]
	EntryID uint64

	// Entries a type define for sort option
	Entries []EntryID
)

func ValidDocID(id DocID) bool {
	return (id <= MaxDocID) && (id >= -MaxDocID)
}
func ValidIdxOrSize(v int) bool {
	return v >= 0 && v < 256
}

// NewConjID
// |--[ reserved(4bit) | size(8bit) | index(8bit)  | negSign(1bit) | docID(43bit)]
func NewConjID(docID DocID, index, size int) ConjID {
	if !ValidDocID(docID) || !ValidIdxOrSize(index) || !ValidIdxOrSize(size) {
		panic(fmt.Errorf("id overflow, id:%d, idx:%d size:%d", docID, index, size))
	}
	negSign := uint64(0)
	if docID < 0 {
		negSign = 1
		docID = -docID
	}
	return ConjID((uint64(size) << 52) | (uint64(index) << 44) | (negSign << 43) | (uint64(docID)))
}

func (id ConjID) Size() int {
	return int((id >> 52) & 0xFF)
}

func (id ConjID) Index() int {
	return int((id >> 44) & 0xFF)
}

func (id ConjID) DocID() DocID {
	negSign := (id >> 43) & 0x1
	docID := DocID(id & MaxDocID)
	if negSign > 0 {
		return -docID
	}
	return docID
}

// NewEntryID encode entry id
// |--          		         ConjID(60bit)                  --|-- empty(3bit) --|--incl/excl(1bit) --|
// |--[ size(8bit) | index(8bit) | negSign(1bit) | docID(43bit)]--|-- empty(3bit) --|--incl/excl(1bit) --|
func NewEntryID(id ConjID, incl bool) EntryID {
	if !incl {
		return EntryID(id << 4)
	}
	return EntryID((id << 4) | 0x01)
}

func (entry EntryID) IsExclude() bool {
	return entry&0x01 == 0
}

func (entry EntryID) IsInclude() bool {
	return entry&0x01 > 0
}

func (entry EntryID) GetConjID() ConjID {
	return ConjID(entry >> 4)
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

// NewKey API
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

func (key Key) String() string {
	return fmt.Sprintf("<%d,%d>", key.GetFieldID(), key.GetValueID())
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
