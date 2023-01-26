package roaringidx

import (
	"fmt"

	"github.com/echoface/be_indexer"
)

const (
	MaxConjunctions = 0xFF
	MaxDocumentID   = 0x7FFFFFFFFFFFFF
)

type (
	// ConjunctionID |- doc(56bit) | idx(8bit) -|
	ConjunctionID uint64
)

func ValidRoaringIdxDocID(id int64) bool {
	return id <= MaxDocumentID && id >= -MaxDocumentID
}

func NewConjunctionID(idx int, doc int64) (ConjunctionID, error) {
	if !be_indexer.ValidIdxOrSize(idx) || !ValidRoaringIdxDocID(doc) {
		return 0, fmt.Errorf("id:%d,%d overflow, doc:[%d,%d] idx:[0,255] ", doc, idx, -MaxDocumentID, MaxDocumentID)
	}
	return ConjunctionID(uint64(doc<<8) | uint64(idx)), nil
}

func (id ConjunctionID) DocID() int64 {
	return int64(id) >> 8
}

func (id ConjunctionID) Idx() uint8 {
	return uint8(id & MaxConjunctions)
}
