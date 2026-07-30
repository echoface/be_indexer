package fst

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/blevesearch/vellum"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newReaderFactory(b []byte) (segment.IndexReader, error) {
	return NewFSTReader(b)
}

// FSTIndex is a vellum-backed term dictionary for exact-term lookups. It maps a
// term to the byte offset of its posting list inside the field's postings block.
//
// Concurrency and allocation: vellum's plain FST.Get allocates a fresh internal
// state struct per call, and its allocation-free FST.Reader is documented as
// single-threaded. To satisfy the IndexReader contract (safe for concurrent
// MatchQuery) while keeping the transducer walk allocation-free, we pool one
// *vellum.Reader per in-flight query: each Reader carries a reusable prealloc
// state that it clears and refills on every Get, so borrowing one from the pool
// removes the per-query state allocation without sharing mutable state across
// goroutines.
type FSTIndex struct {
	fst        *vellum.FST
	readerPool sync.Pool
}

// NewFSTReader loads an FSTIndex from a serialized vellum block. Load is
// zero-copy: the returned FST reads directly from the mapped block bytes, so the
// slice must outlive the reader (guaranteed by SegmentReader's block ownership).
func NewFSTReader(b []byte) (*FSTIndex, error) {
	if len(b) == 0 {
		return &FSTIndex{}, nil
	}
	f, err := vellum.Load(b)
	if err != nil {
		return nil, fmt.Errorf("fst: load: %w", err)
	}
	idx := &FSTIndex{fst: f}
	idx.readerPool.New = func() any {
		// FST.Reader never returns a non-nil error in vellum v1.2.0; a nil Reader
		// simply routes MatchQuery through the (allocating) FST.Get fallback.
		r, _ := f.Reader()
		return r
	}
	return idx, nil
}

// lookup resolves term -> (offset, exists) using a pooled single-threaded Reader
// when available (allocation-free state reuse), falling back to FST.Get.
func (r *FSTIndex) lookup(term string) (uint64, bool, error) {
	key := stringToBytes(term)
	if rd, _ := r.readerPool.Get().(*vellum.Reader); rd != nil {
		off, exists, err := rd.Get(key)
		r.readerPool.Put(rd)
		return off, exists, err
	}
	return r.fst.Get(key)
}

// stringToBytes returns the string's backing bytes without copying.
//
// A plain []byte(term) conversion heap-allocates on every query. This is safe
// only because the callee, FST.Get, treats the key as read-only (it walks the
// transducer byte by byte) and never retains a reference to the slice. The
// returned slice MUST NOT be mutated or stored.
func stringToBytes(s string) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// MatchQuery looks up an exact term and returns a posting cursor over its
// posting list, or an empty result if the term is not indexed. The posting count
// is read from the FlatPostingList header at the resolved offset, so the FST
// value carries only the offset.
func (r *FSTIndex) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("fst: query must be string, got %T", query)
	}
	if r.fst == nil {
		return nil, nil
	}
	off, exists, err := r.lookup(term)
	if err != nil {
		return nil, fmt.Errorf("fst: get %q: %w", term, err)
	}
	if !exists {
		return nil, nil
	}
	if off > uint64(len(ctx.Pl)) {
		return nil, fmt.Errorf("fst: term %q offset %d out of range (block=%d)", term, off, len(ctx.Pl))
	}
	pl, err := segment.NewFlatPostingList(ctx.Pl[off:])
	if err != nil {
		return nil, fmt.Errorf("fst: term %q posting: %w", term, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}
