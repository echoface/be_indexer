package roaringidx

import "github.com/echoface/be_indexer/util"

const (
	MaxConjunctions = 0xFF

	MaxDocumentBits = 56
	MaxDocumentID   = 0xFFFFFFFFFFFFFF
)

type (
	// ConjunctionID [8bit idx + 56 bit document id]
	ConjunctionID uint64
)

func NewConjunctionID(idx int, doc int64) ConjunctionID {
	util.PanicIf(idx > MaxConjunctions, "conjunction idx overflow, max:0xFF")
	return ConjunctionID(uint64(idx)<<MaxDocumentBits | uint64(doc&MaxDocumentID))
}

// DocID [8bit idx + 56bit document]
func (id ConjunctionID) DocID() int64 {
	return int64(uint64(id) & MaxDocumentID)
}

func (id ConjunctionID) Idx() uint8 {
	return uint8(uint64(id) >> MaxDocumentBits & MaxConjunctions)
}
