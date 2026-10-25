package core

import (
	"fmt"
	"testing"
)

// TestFieldCursorSkipTo_DuplicateHeads verifies that SkipTo advances past ALL
// stale heap heads, not just one: the returned id is the true minimum >= target
// across every iterator.
func TestFieldCursorSkipTo_DuplicateHeads(t *testing.T) {
	// Multiple iterators share identical heads below the target; a single-pop
	// implementation would return a stale minimum here.
	shared := []EntryID{10, 10, 10, 30, 50}
	iters := make([]PostingIterator, len(shared))
	for i, eid := range shared {
		iters[i] = NewSliceIterator(NewTerm("f", 0), []EntryID{eid, 100})
	}
	fc := NewFieldCursor(iters...)

	for _, target := range []EntryID{0, 10, 11, 30, 31, 50, 51, 100, 101} {
		got := fc.SkipTo(target)
		want := NULLENTRY
		for _, it := range iters {
			if cur := it.SkipTo(target); cur != NULLENTRY && (want == NULLENTRY || cur < want) {
				want = cur
			}
		}
		if got != want {
			t.Fatalf("SkipTo(%d): got %d, want %d", target, got, want)
		}
	}
}

func TestFieldCursorsSort_AllocationFree(t *testing.T) {
	fcs := NewFieldCursors(8)
	for i := 0; i < 8; i++ {
		fcs.Append(NewFieldCursor(NewSliceIterator(
			NewTerm("f", 0), []EntryID{EntryID(i * 3)},
		)))
	}
	fcs.items[0], fcs.items[7] = fcs.items[7], fcs.items[0]

	before := testing.AllocsPerRun(100, func() { fcs.Sort() })
	if before != 0 {
		t.Fatalf("Sort allocated: %v allocs/op", before)
	}
	for i := 1; i < fcs.Len(); i++ {
		if fcs.PeekAt(i-1) > fcs.PeekAt(i) {
			t.Fatalf("not sorted at %d: %d > %d", i, fcs.PeekAt(i-1), fcs.PeekAt(i))
		}
	}
}

func BenchmarkFieldCursorSkipTo_DuplicateHeads(b *testing.B) {
	for _, n := range []int{2, 8, 32} {
		b.Run(fmt.Sprintf("iters=%d", n), func(b *testing.B) {
			eids := make([]EntryID, 512)
			for i := range eids {
				eids[i] = EntryID(i / 4 * 4) // dense duplicates across iterators
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				iters := make([]PostingIterator, n)
				for j := range iters {
					iters[j] = NewSliceIterator(NewTerm("f", 0), eids)
				}
				fc := NewFieldCursor(iters...)
				for target := EntryID(0); target < 512; target += 4 {
					fc.SkipTo(target)
				}
			}
		})
	}
}
