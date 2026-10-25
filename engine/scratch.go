package engine

import (
	"sync"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
)

// retrieveScratch holds every query-scoped buffer used by one retrieval. A
// single pool recycles them across queries; the slices keep their capacity and
// only logical length is reset.
type retrieveScratch struct {
	encoded   []encodedField
	queries   []parser.EncodedQuery // backing array for all encoded fields
	wildcards []core.PostingIterator
	iters     []core.PostingIterator // backing array for all per-field cursors
	cursors   *core.FieldCursors
}

var scratchPool = sync.Pool{
	New: func() any { return new(retrieveScratch) },
}

func pickScratch() *retrieveScratch {
	return scratchPool.Get().(*retrieveScratch)
}

// release clears references so a pooled scratch does not pin caller objects,
// then returns it to the pool.
func (s *retrieveScratch) release() {
	// Clear the whole capacity, not only len: a slot beyond len still holds an
	// interface pointer and would pin the previous query's iterators while the
	// scratch sits in the pool.
	clear(s.encoded[:cap(s.encoded)])
	clear(s.queries[:cap(s.queries)])
	clear(s.wildcards[:cap(s.wildcards)])
	clear(s.iters[:cap(s.iters)])
	s.encoded, s.queries = s.encoded[:0], s.queries[:0]
	s.wildcards, s.iters = s.wildcards[:0], s.iters[:0]
	if s.cursors != nil {
		s.cursors.ClearRecycled()
	}
	scratchPool.Put(s)
}

// getEncoded returns the encoded-field buffer reset to capacity hint.
func (s *retrieveScratch) getEncoded(hint int) []encodedField {
	if cap(s.encoded) < hint {
		s.encoded = make([]encodedField, 0, hint)
	}
	return s.encoded[:0]
}

func (s *retrieveScratch) getWildcards(hint int) []core.PostingIterator {
	if cap(s.wildcards) < hint {
		s.wildcards = make([]core.PostingIterator, 0, hint)
	}
	return s.wildcards[:0]
}

func (s *retrieveScratch) getIters(hint int) []core.PostingIterator {
	if cap(s.iters) < hint {
		s.iters = make([]core.PostingIterator, 0, hint)
	}
	return s.iters[:0]
}

func (s *retrieveScratch) getCursors(hint int) *core.FieldCursors {
	if s.cursors == nil {
		s.cursors = core.NewFieldCursors(hint)
	}
	s.cursors.Reset(hint)
	return s.cursors
}
