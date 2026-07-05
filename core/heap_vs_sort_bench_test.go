package core

import (
	"fmt"
	"sort"
	"testing"
)

func makeBenchEntries(n int) []EntryID {
	eids := make([]EntryID, n)
	for i := range eids {
		eids[i] = NewEntryID(NewConjID(DocID(i*10), i%250, i%250+1), i%2 == 0)
	}
	return eids
}

func benchFreshIters(n int) []PostingIterator {
	iters := make([]PostingIterator, n)
	for i := range iters {
		eids := makeBenchEntries(1000)
		for j := range eids {
			eids[j] += EntryID(i * 100)
		}
		iters[i] = NewSliceIterator(NewTerm("idx", 0), eids)
	}
	return iters
}

// --------------- sort.Slice comparison (old approach) ---------------

type fieldCursorSort_ struct {
	Iters []PostingIterator
}

func newFieldCursorSort_(iters []PostingIterator) fieldCursorSort_ {
	for i := 0; i < len(iters); i++ {
		if iters[i] == nil || iters[i].Current().IsNULLEntry() {
			iters = append(iters[:i], iters[i+1:]...)
			i--
		}
	}
	fc := fieldCursorSort_{Iters: iters}
	sort.Slice(fc.Iters, func(a, b int) bool {
		return fc.Iters[a].Current() < fc.Iters[b].Current()
	})
	return fc
}

func (f *fieldCursorSort_) skipToSort(id EntryID) EntryID {
	if len(f.Iters) == 0 {
		return NULLENTRY
	}
	res := f.Iters[0].SkipTo(id)
	sort.Slice(f.Iters, func(i, j int) bool {
		return f.Iters[i].Current() < f.Iters[j].Current()
	})
	return res
}

// BenchmarkFieldCursor_HeapVsSort compares SkipTo cost.
func BenchmarkFieldCursor_HeapVsSort(b *testing.B) {
	for _, n := range []int{5, 10, 50, 100} {
		b.Run(fmt.Sprintf("heap_iters=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				iters := benchFreshIters(n)
				fc := NewFieldCursor(iters...)
				for j := 0; j < 100; j++ {
					fc.SkipTo(EntryID(j * 50))
				}
			}
		})

		b.Run(fmt.Sprintf("sort_iters=%d", n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				iters := benchFreshIters(n)
				fc := newFieldCursorSort_(iters)
				for j := 0; j < 100; j++ {
					fc.skipToSort(EntryID(j * 50))
				}
			}
		})
	}
}
