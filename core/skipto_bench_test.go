package core

import (
	"fmt"
	"testing"
)

// SkipTo Optimization: Galloping Search
//
// The mergeCursors loop advances wildcard/field cursors through sorted
// EntryID arrays. ~90% of SkipTo calls are "seq +1" — the next target
// is the immediate next entry.
//
// Baseline (binary search): O(log N) = 17 probes for N=100K, every call.
// Galloping: fast-path check first. If next entry >= target (90% case),
//            returns in 1 probe. Otherwise exponential probe locates a
//            narrow search window, then binary search within that window.
//
// Why not Eytzinger (BFS-ordered branch-free search)?
//   - Requires full array reorder → breaks zero-copy mmap views.
//   - Destroys sorted order → every Current()→SkipTo is O(log N), no
//     fast-path possible for sequential access.
//   - Build may fail for non-power-of-2-minus-1 sizes without padding.
//   - The dominant seq(+1) pattern does not benefit from branch-free search
//     because galloping reduces it to 1 comparison (no branches to predict).
//
// Why not SIMD?
//   - Posting lists are small (~500-50000 entries), SIMD overhead dominates.
//   - Go has no SIMD intrinsics; cgo boundary cost is prohibitive.
//   - Galloping achieves 2-17x improvement with 0 memory and pure Go.
//
// Conclusion: galloping is the highest-ROI SkipTo optimization — zero
// memory overhead, mmap-compatible, pure Go, and delivers O(1) for the
// 90% dominant access pattern.
// ---------------------------------------------------------------

// ---------------------------------------------------------------
// Galloping search (as used in SliceIterator and flatPostingCursor)
// ---------------------------------------------------------------

func gallopingSearch(data Entries, cursor int, target EntryID) int {
	n := len(data)
	if cursor >= n {
		return n
	}
	if data[cursor] >= target {
		return cursor
	}
	lo := cursor + 1
	hi := lo
	step := 1
	for hi < n && data[hi] < target {
		lo = hi + 1
		step *= 2
		hi = cursor + step
	}
	if hi >= n {
		hi = n - 1
	}
	for lo <= hi {
		mid := lo + (hi-lo)/2
		if data[mid] < target {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func baselineSearch(data Entries, cursor int, target EntryID) int {
	left, right := cursor, len(data)-1
	for left <= right {
		mid := left + (right-left)/2
		if data[mid] < target {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	return left
}

// ---------------------------------------------------------------
// Correctness
// ---------------------------------------------------------------

func TestGallopingMatchesBaseline(t *testing.T) {
	cases := []struct {
		name string
		e    Entries
	}{
		{"dense100", makeK0WildcardEntries(100, 1)},
		{"sparse100", makeK0WildcardEntries(100, 1000)},
		{"multiIdx", makeK0WildcardEntriesMultiIndex(50, 8)},
		{"empty", nil},
		{"single", makeK0WildcardEntries(1, 1)},
		{"two", makeK0WildcardEntries(2, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := len(tc.e)
			targets := make([]EntryID, 0, n*2+3)
			for _, e := range tc.e {
				targets = append(targets, e, e+1)
			}
			if n > 0 {
				targets = append(targets, tc.e[n-1]+2)
			}
			targets = append(targets, 0, EntryID(^uint64(0)>>1))
			cursors := []int{0}
			if n > 4 {
				cursors = append(cursors, n/4, n/2, 3*n/4, n-1)
			}
			if n > 0 {
				cursors = append(cursors, n-1, n)
			}
			for _, cursor := range cursors {
				for _, tgt := range targets {
					b := baselineSearch(tc.e, cursor, tgt)
					g := gallopingSearch(tc.e, cursor, tgt)
					if b != g {
						t.Fatalf("cursor=%d target=%d: baseline=%d galloping=%d",
							cursor, tgt, b, g)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------
// Benchmarks: galloping vs baseline
// ---------------------------------------------------------------

type benchFixture2 struct {
	entries Entries
}

func newBenchFixture2(n int, docStride int64) benchFixture2 {
	return benchFixture2{entries: makeK0WildcardEntries(n, docStride)}
}

func runBaselineSeq(data Entries, targets []EntryID) (sink int) {
	cursor := 0
	for _, tgt := range targets {
		cursor = baselineSearch(data, cursor, tgt)
	}
	return cursor
}

func runGallopingSeq(data Entries, targets []EntryID) (sink int) {
	cursor := 0
	for _, tgt := range targets {
		cursor = gallopingSearch(data, cursor, tgt)
	}
	return cursor
}

func BenchmarkSkipToComparison(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000, 1_000_000}
	patterns := []struct {
		name  string
		make  func(e Entries) []EntryID
	}{
		{"seq", func(e Entries) []EntryID { return e }},
		{"stride16", func(e Entries) []EntryID { return strideTargets(e, 16) }},
		{"stride256", func(e Entries) []EntryID { return strideTargets(e, 256) }},
		{"jump64", func(e Entries) []EntryID { return jumpTargets(e, 64) }},
	}

	for _, n := range sizes {
		fx := newBenchFixture2(n, 1)
		for _, p := range patterns {
			targets := p.make(fx.entries)
			if len(targets) == 0 {
				continue
			}

			b.Run(fmt.Sprintf("n=%d/%s/baseline", n, p.name), func(b *testing.B) {
				var sink int
				for i := 0; i < b.N; i++ {
					sink = runBaselineSeq(fx.entries, targets)
				}
				_ = sink
			})

			b.Run(fmt.Sprintf("n=%d/%s/galloping", n, p.name), func(b *testing.B) {
				var sink int
				for i := 0; i < b.N; i++ {
					sink = runGallopingSeq(fx.entries, targets)
				}
				_ = sink
			})
		}
	}
}

// BenchmarkSeqAdvance isolates the 90% dominant case:
// cursor at position k, SkipTo to entry at position k (seq +1 equivalent).
// Uses the real SliceIterator to measure iterator overhead.
func BenchmarkSeqAdvance(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000, 1_000_000}
	for _, n := range sizes {
		entries := makeK0WildcardEntries(n, 1)
		targets := make([]EntryID, n)
		copy(targets, entries)

		b.Run(fmt.Sprintf("n=%d/seq/galloping_sliceiter", n), func(b *testing.B) {
			b.ReportAllocs()
			var sink EntryID
			for i := 0; i < b.N; i++ {
				it := NewSliceIterator(WildcardTerm, entries)
				for _, tgt := range targets {
					sink = it.SkipTo(tgt)
				}
			}
			_ = sink
		})
	}
}

// BenchmarkSeqAdvanceIsolated measures raw galloping vs baseline seq(+1)
// cost per SkipTo call, without iterator wrapper overhead.
func BenchmarkSeqAdvanceIsolated(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		entries := makeK0WildcardEntries(n, 1)
		targets := make([]EntryID, len(entries))
		copy(targets, entries)

		b.Run(fmt.Sprintf("n=%d/seq/baseline", n), func(b *testing.B) {
			b.ReportAllocs()
			data := entries
			var cursor int
			var sink int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cursor = 0
				for _, tgt := range targets {
					cursor = baselineSearch(data, cursor, tgt)
				}
			}
			_ = sink
		})

		b.Run(fmt.Sprintf("n=%d/seq/galloping", n), func(b *testing.B) {
			b.ReportAllocs()
			data := entries
			var cursor int
			var sink int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cursor = 0
				for _, tgt := range targets {
					cursor = gallopingSearch(data, cursor, tgt)
				}
			}
			_ = sink
		})
	}
}
