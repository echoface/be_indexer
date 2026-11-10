package core

import (
	"fmt"
)

const (
	MaxDocID = 0x7FFFFFFFFFF

	// EndEntryID marks an exhausted cursor and sorts after every valid entry.
	EndEntryID EntryID = 0xFFFFFFFFFFFFFFFF
)

type (
	// ConjID identifies a conjunction by DocID and index and encodes its K.
	// [ reserved(4bit) | K(8bit) | index(8bit) | negSign(1bit) | docID(43bit) ]
	ConjID uint64

	// EntryID is a sorting key for a conjunction and its Include/Exclude polarity.
	// The same key can occur in different posting lists; it does not encode a term.
	// [--ConjID(60bit)--|--empty(3bit)--|--incl/excl(1bit)--]
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

// NewConjID encodes a document, conjunction index, and Include count (size = K).
// [ reserved(4bit) | K(8bit) | index(8bit) | negSign(1bit) | docID(43bit) ]
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

// IncludeCount decodes K from bits 52-59, without reading the original rule.
func (id ConjID) IncludeCount() int {
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

func (id ConjID) String() string {
	return fmt.Sprintf("<%d,%d,%d>", id.DocID(), id.Index(), id.IncludeCount())
}

// NewEntryID encode entry id
// [--ConjID(60bit)--|--empty(3bit)--|--incl/excl(1bit)--]
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

// ConjID removes the reserved bits and Include/Exclude bit to recover the ID.
func (entry EntryID) ConjID() ConjID {
	return ConjID(entry >> 4)
}

// IsEnd reports whether entry is the exhausted-cursor sentinel.
func (entry EntryID) IsEnd() bool {
	return entry == EndEntryID
}

func (entry EntryID) DocString() string {
	if entry.IsEnd() {
		return "nil"
	}
	return fmt.Sprintf("%d#%d", entry.ConjID().DocID(), entry&0x01)
}

// Len Entries sort API
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

// KStartEntryID returns the minimum EntryID for a given conjunction size K.
// K occupies bits 56-63 of EntryID due to the ConjID layout and the <<4 shift:
//
//	EntryID = (ConjID << 4) | incl/excl
//	ConjID  = [ reserved(4) | K(8) | index(8) | negSign(1) | docID(43) ]
//	→ K shifted to bits 56-63 in EntryID.
//
// All EntryIDs with size K fall in [KStartEntryID(K), KStartEntryID(K+1)).
func KStartEntryID(k int) EntryID {
	return EntryID(uint64(k) << 56)
}
