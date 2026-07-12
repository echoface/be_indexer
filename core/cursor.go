package core

import (
	"container/heap"
	"sort"
)

// SliceIterator a wrap for slice EntryID
type SliceIterator struct {
	cursor int
	term   Term
	EIDs   []EntryID
}

func NewSliceIterator(term Term, eids []EntryID) *SliceIterator {
	return &SliceIterator{
		cursor: 0,
		term:   term,
		EIDs:   eids,
	}
}

func (s *SliceIterator) Term() Term {
	return s.term
}

func (s *SliceIterator) Current() EntryID {
	if s.cursor >= len(s.EIDs) {
		return NULLENTRY
	}
	return s.EIDs[s.cursor]
}

func (s *SliceIterator) SkipTo(target EntryID) EntryID {
	n := len(s.EIDs)
	if s.cursor >= n {
		return NULLENTRY
	}
	// Galloping search: exponential probe to locate a narrow search window,
	// then binary search within that window. Collapses to a single check
	// for sequential advances (the dominant pattern in mergeCursors).
	lo := s.cursor
	if s.EIDs[lo] >= target {
		return s.EIDs[lo]
	}
	lo++
	hi := lo
	step := 1
	for hi < n && s.EIDs[hi] < target {
		lo = hi + 1
		step *= 2
		hi = s.cursor + step
	}
	if hi >= n {
		hi = n - 1
	}
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if s.EIDs[mid] < target {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	s.cursor = lo
	return s.Current()
}

func (s *SliceIterator) ReachEnd() bool {
	return s.cursor >= len(s.EIDs)
}

// iterHeap is a min-heap of PostingIterator ordered by Current() EntryID.
// It replaces sort.Slice in FieldCursor, reducing SkipTo from O(n log n) to O(log n).
type iterHeap []PostingIterator

func (h iterHeap) Len() int           { return len(h) }
func (h iterHeap) Less(i, j int) bool { return h[i].Current() < h[j].Current() }
func (h iterHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *iterHeap) Push(x any)        { *h = append(*h, x.(PostingIterator)) }
func (h *iterHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// FieldCursor maintains a heap-ordered set of PostingIterators. The minimum
// EntryID is always at the top. SkipTo pops the minimum, advances it past the
// target id, and pushes it back — O(log n) instead of the previous O(n log n).
type FieldCursor struct {
	Iters iterHeap
}

func NewFieldCursor(iters ...PostingIterator) FieldCursor {
	fc := FieldCursor{Iters: make(iterHeap, 0, len(iters))}
	for _, it := range iters {
		if it != nil && !it.Current().IsNULLEntry() {
			fc.Iters = append(fc.Iters, it)
		}
	}
	heap.Init(&fc.Iters)
	return fc
}

func (f *FieldCursor) GetCurEntryID() EntryID {
	if len(f.Iters) == 0 {
		return NULLENTRY
	}
	return f.Iters[0].Current()
}

func (f *FieldCursor) SkipTo(id EntryID) EntryID {
	if len(f.Iters) == 0 {
		return NULLENTRY
	}
	it := heap.Pop(&f.Iters).(PostingIterator)
	it.SkipTo(id)
	heap.Push(&f.Iters, it)
	return f.Iters[0].Current()
}

// ReachEnd reports whether all iterators in this cursor have exhausted.
// An empty cursor (no iterators at all) is treated as exhausted.
func (f *FieldCursor) ReachEnd() bool {
	if len(f.Iters) == 0 {
		return true
	}
	for _, it := range f.Iters {
		if !it.ReachEnd() {
			return false
		}
	}
	return true
}

func (f *FieldCursor) DumpInfo() []string {
	var res []string
	for _, it := range f.Iters {
		if it == nil || it.Current().IsNULLEntry() {
			continue
		}
		res = append(res, it.Current().DocString())
	}
	return res
}

// FieldCursors is a wrapper over []FieldCursor with sort.Slice ordering.
// Benchmark shows sort.Slice is faster than heap for outer FieldCursors
// because Go's sort.Slice is highly optimized for slice sizes < 1000.
type FieldCursors struct {
	items []FieldCursor
}

func NewFieldCursors(cap int) *FieldCursors {
	return &FieldCursors{items: make([]FieldCursor, 0, cap)}
}

func (fcs *FieldCursors) Len() int { return len(fcs.items) }

func (fcs *FieldCursors) Append(fc FieldCursor) {
	fcs.items = append(fcs.items, fc)
}

func (fcs *FieldCursors) Sort() {
	sort.Slice(fcs.items, func(i, j int) bool {
		return fcs.items[i].GetCurEntryID() < fcs.items[j].GetCurEntryID()
	})
}

// Peek returns the smallest EntryID among all FieldCursors.
func (fcs *FieldCursors) Peek() EntryID {
	if len(fcs.items) == 0 {
		return NULLENTRY
	}
	return fcs.items[0].GetCurEntryID()
}

// PeekAt returns the k-th smallest EntryID (0-indexed).
func (fcs *FieldCursors) PeekAt(k int) EntryID {
	if k < 0 || k >= len(fcs.items) {
		return NULLENTRY
	}
	return fcs.items[k].GetCurEntryID()
}

// AdvanceFirst advances the first k elements past nextID, then re-sorts.
func (fcs *FieldCursors) AdvanceFirst(k int, nextID EntryID) {
	if k <= 0 || len(fcs.items) == 0 {
		return
	}
	for i := 0; i < k && i < len(fcs.items); i++ {
		fcs.items[i].SkipTo(nextID)
	}
	fcs.Sort()
}

// ShortCircuitAfter conditionally advances all elements after the first k
// whose current EntryID is less than nextID, then re-sorts.
//
// This optimizes the exclude path of mergeCursors: when the first k cursors
// match an exclude EntryID, all remaining cursors that still point to a
// position before the current conjunction are skipped forward. Without this
// optimization they would stall on stale positions and cause false matches
// in later loop iterations.
func (fcs *FieldCursors) ShortCircuitAfter(k int, nextID EntryID) {
	n := len(fcs.items)
	if k >= n {
		return
	}
	for i := k; i < n; i++ {
		if fcs.items[i].GetCurEntryID() < nextID {
			fcs.items[i].SkipTo(nextID)
		}
	}
	fcs.Sort()
}

// CompactLast removes exhausted cursors from the end of the list.
// After Sort(), exhausted cursors (those with only NULLENTRY entries)
// naturally move to the right end. Call Sort() before CompactLast().
func (fcs *FieldCursors) CompactLast() {
	for fcs.Len() > 0 {
		last := fcs.Len() - 1
		if fcs.items[last].ReachEnd() {
			fcs.items = fcs.items[:last]
		} else {
			break
		}
	}
}
