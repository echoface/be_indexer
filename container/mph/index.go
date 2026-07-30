package mph

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/alecthomas/mph"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newReaderFactory(b []byte) (segment.IndexReader, error) {
	return NewMPHReader(b)
}

// MPHIndex is a minimal-perfect-hash container for exact-term lookups.
type MPHIndex struct {
	chd *mph.CHD
}

// NewMPHReader creates an MPHIndex from a serialized CHD block.
func NewMPHReader(b []byte) (*MPHIndex, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("mph: blob too short: len=%d", len(b))
	}
	chd, err := mph.Mmap(b)
	if err != nil {
		return nil, fmt.Errorf("mph: mmap CHD: %w", err)
	}
	return &MPHIndex{chd: chd}, nil
}

func decodePostingRef(val []byte) (segment.PostingRef, error) {
	if len(val) < 12 {
		return segment.PostingRef{}, fmt.Errorf("mph: corrupted posting ref: len=%d", len(val))
	}
	return segment.PostingRef{
		Offset: binary.LittleEndian.Uint64(val[0:8]),
		Count:  binary.LittleEndian.Uint32(val[8:12]),
	}, nil
}

// stringToBytes returns the string's backing bytes without copying.
//
// A plain []byte(term) conversion heap-allocates on every query, which violates
// the zero-allocation contract of the retrieval hot path (engine.Retrieve).
// This is safe only because the callee, CHD.Get, treats the key as read-only
// (it hashes the bytes and does a bytes.Compare) and never retains a reference
// to the slice. The returned slice MUST NOT be mutated or stored.
func stringToBytes(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func (r *MPHIndex) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("mph: query must be string, got %T", query)
	}
	val := r.chd.Get(stringToBytes(term))
	if val == nil {
		return nil, nil
	}
	ref, err := decodePostingRef(val)
	if err != nil {
		return nil, err
	}
	pl, err := segment.NewPostingListAt(ctx.Pl, ref)
	if err != nil {
		return nil, fmt.Errorf("mph: term %q posting: %w", term, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}
