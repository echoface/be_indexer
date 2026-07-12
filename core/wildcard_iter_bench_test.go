// Purpose: Compare SliceIterator (binary search on []EntryID) vs
// RoaringEntryIter (roaring64.IntPeekable64.AdvanceIfNeeded) as TermIterator
// backends for wildcard (Z-list) EntryIDs.
//
// The comparison covers three dimensions:
//   1. Storage — raw []EntryID vs roaring64 heap vs roaring64 serialized
//   2. Build (load-time) — slice copy vs roaring_from_slice vs roaring_unmarshal
//   3. Query (hot-path SkipTo) — seq / stride / jump / random-monotone patterns
//
// --- Key findings (Intel i7-9750H, darwin/amd64) ---
//
// Storage (N=100K wildcards):
//   dense (consecutive DocIDs)   slice=800KB  roaring_heap=200KB (~25%)  roaring_ser=200KB (~25%)
//   sparse (DocID stride=1000)   slice=800KB  roaring_heap=249KB (~31%)  roaring_ser=395KB (~49%)
//   multi_idx8                   slice=800KB  roaring_heap=200KB (~25%)  roaring_ser=202KB (~25%)
//   multi_idx64                  slice=800KB  roaring_heap=204KB (~25%)  roaring_ser=214KB (~27%)
//
// Build / load (N=100K):
//   slice_copy         76 μs, 800KB alloc,   1 alloc   (baseline copy of EntryID data)
//   roaring_from_slice 958 μs, 1.8MB alloc, 368 allocs  (12.6x slower; don't do this on load path)
//   roaring_unmarshal  38 μs, 202KB alloc,  66 allocs  (2.0x faster than slice_copy; 4x less memory)
//
// Build / load (N=1M):
//   slice_copy         1.1 ms,  8MB alloc,    1 alloc
//   roaring_unmarshal  0.5 ms,  2MB alloc,  507 allocs  (2.2x faster, 4x less memory)
//
// Query SkipTo (N=100K dense, seq full-scan = mergeCursors dominant pattern):
//   Slice     ~3.9 ms, 0 allocs
//   Roaring   ~2.1 ms, 928B alloc, 28 allocs   (1.86x faster)
//
// Query SkipTo (N=1M dense, seq):
//   Slice     ~49 ms,  0 allocs
//   Roaring   ~19 ms,  8KB alloc, 248 allocs   (2.6x faster)
//
// Query SkipTo (N=100K, jump64 = sparse random across key space):
//   Slice     ~2.2 μs, 0 allocs                 (2x faster — binary search wins for few calls)
//   Roaring   ~4.5 μs, 928B alloc, 28 allocs
//
// Design implications:
//   - Roaring unmarshal is the clear winner for load-side cold start:
//     faster deserialize + smaller memory footprint vs raw EntryID slice.
//   - Roaring SkipTo is ~2x faster for the seq/stride access pattern that
//     dominates mergeCursors (continuous advancement through wildcard entries).
//   - Slice wins on sparse random access (few SkipTo calls across large ranges)
//     AND has zero per-query allocations — better for latency-sensitive paths.
//   - roaring_from_slice must be avoided on the load path (12x overhead).
//     A roaring wildcard sidecar must be pre-built and pre-serialized at build time.
//   - Neither iterator directly solves the cross-segment wildcard merge problem.
//     The FieldCursor internal heap already does lazy K-way merge at query time,
//     eliminating the need for load-time merge entirely.
//
package core

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// --- Z-list fixtures (K=0 EntryIDs, same encoding as builder export) ---

// makeK0WildcardEntries builds n sorted K=0 include EntryIDs.
// docStride controls DocID spacing (1 = dense consecutive docs; larger = sparser).
// conjIndex is fixed at 0 to match the common pure-exclude / always-match case.
func makeK0WildcardEntries(n int, docStride int64) Entries {
	if n <= 0 {
		return nil
	}
	if docStride <= 0 {
		docStride = 1
	}
	out := make(Entries, n)
	for i := 0; i < n; i++ {
		docID := DocID(int64(i)*docStride + 1)
		out[i] = NewEntryID(NewConjID(docID, 0, 0), true)
	}
	return out
}

// makeK0WildcardEntriesMultiIndex mixes conj indices so high bits of EntryID
// spread across more roaring64 high-key buckets (worst-ish layout for roaring64).
func makeK0WildcardEntriesMultiIndex(n int, maxIndex int) Entries {
	if n <= 0 {
		return nil
	}
	if maxIndex < 1 {
		maxIndex = 1
	}
	out := make(Entries, n)
	for i := 0; i < n; i++ {
		docID := DocID(i + 1)
		idx := i % maxIndex
		out[i] = NewEntryID(NewConjID(docID, idx, 0), true)
	}
	// EntryIDs are ordered by full uint64, not by docID alone when index varies.
	// NewConjID packs index above docID, so re-sort for iterator invariants.
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// roaringFromEntries builds a roaring64 bitmap from sorted EntryIDs.
func roaringFromEntries(e Entries) *roaring64.Bitmap {
	bm := roaring64.New()
	// AddMany is faster than Add in a loop when available.
	vals := make([]uint64, len(e))
	for i, eid := range e {
		vals[i] = uint64(eid)
	}
	bm.AddMany(vals)
	return bm
}

// roaringEntryIter adapts roaring64.IntPeekable64 to PostingIterator SkipTo semantics:
// Current is the first EntryID >= last SkipTo/initial position; SkipTo advances to >= id.
type roaringEntryIter struct {
	it  roaring64.IntPeekable64
	cur EntryID
	end bool
}

func newRoaringEntryIter(bm *roaring64.Bitmap) *roaringEntryIter {
	it := bm.Iterator()
	r := &roaringEntryIter{it: it}
	if !it.HasNext() {
		r.end = true
		r.cur = NULLENTRY
		return r
	}
	r.cur = EntryID(it.Next())
	return r
}

func (r *roaringEntryIter) Term() Term { return WildcardTerm }

func (r *roaringEntryIter) Current() EntryID {
	if r.end {
		return NULLENTRY
	}
	return r.cur
}

func (r *roaringEntryIter) ReachEnd() bool { return r.end }

// SkipTo advances to the first entry >= id (same contract as SliceIterator.SkipTo).
func (r *roaringEntryIter) SkipTo(id EntryID) EntryID {
	if r.end {
		return NULLENTRY
	}
	if r.cur >= id {
		return r.cur
	}
	// AdvanceIfNeeded leaves PeekNext() >= minval when HasNext.
	// We already consumed cur via Next() at construction / previous SkipTo,
	// so advance the underlying stream until PeekNext >= id, then Next.
	r.it.AdvanceIfNeeded(uint64(id))
	if !r.it.HasNext() {
		r.end = true
		r.cur = NULLENTRY
		return NULLENTRY
	}
	// After AdvanceIfNeeded, PeekNext is >= id. Consume it as Current.
	r.cur = EntryID(r.it.Next())
	return r.cur
}

// --- correctness: roaring adapter must match SliceIterator on SkipTo sequences ---

func TestRoaringEntryIter_MatchesSliceIterator(t *testing.T) {
	cases := []struct {
		name string
		e    Entries
	}{
		{"dense_1k", makeK0WildcardEntries(1000, 1)},
		{"sparse_1k", makeK0WildcardEntries(1000, 1000)},
		{"multi_idx", makeK0WildcardEntriesMultiIndex(500, 8)},
		{"empty", nil},
		{"single", makeK0WildcardEntries(1, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sliceIt := NewSliceIterator(WildcardTerm, tc.e)
			bm := roaringFromEntries(tc.e)
			rbIt := newRoaringEntryIter(bm)

			// Full sequential scan via SkipTo(cur+1) style.
			for !sliceIt.ReachEnd() {
				if rbIt.ReachEnd() {
					t.Fatalf("roaring ended early at slice cur=%d", sliceIt.Current())
				}
				if sliceIt.Current() != rbIt.Current() {
					t.Fatalf("current mismatch: slice=%d roaring=%d", sliceIt.Current(), rbIt.Current())
				}
				next := sliceIt.Current() + 1
				sc := sliceIt.SkipTo(next)
				rc := rbIt.SkipTo(next)
				if sc != rc {
					t.Fatalf("SkipTo(%d) mismatch: slice=%d roaring=%d", next, sc, rc)
				}
			}
			if !rbIt.ReachEnd() {
				t.Fatalf("roaring has leftover: %d", rbIt.Current())
			}

			// Jump sequence from a fresh pair of iterators.
			if len(tc.e) == 0 {
				return
			}
			sliceIt = NewSliceIterator(WildcardTerm, tc.e)
			rbIt = newRoaringEntryIter(bm)
			targets := []EntryID{
				tc.e[0],
				tc.e[len(tc.e)/4],
				tc.e[len(tc.e)/2],
				tc.e[len(tc.e)*3/4],
				tc.e[len(tc.e)-1],
				tc.e[len(tc.e)-1] + 1, // past end
			}
			for _, tgt := range targets {
				if sliceIt.SkipTo(tgt) != rbIt.SkipTo(tgt) {
					t.Fatalf("jump SkipTo(%d): slice=%d roaring=%d",
						tgt, sliceIt.Current(), rbIt.Current())
				}
			}
		})
	}
}

// --- size report (not a timed bench; printed once) ---

func TestWildcardZList_SizeReport(t *testing.T) {
	type row struct {
		label string
		n     int
		make  func(n int) Entries
	}
	rows := []row{
		{"dense_stride1", 1_000, func(n int) Entries { return makeK0WildcardEntries(n, 1) }},
		{"dense_stride1", 10_000, func(n int) Entries { return makeK0WildcardEntries(n, 1) }},
		{"dense_stride1", 100_000, func(n int) Entries { return makeK0WildcardEntries(n, 1) }},
		{"dense_stride1", 1_000_000, func(n int) Entries { return makeK0WildcardEntries(n, 1) }},
		{"sparse_stride1k", 1_000, func(n int) Entries { return makeK0WildcardEntries(n, 1000) }},
		{"sparse_stride1k", 10_000, func(n int) Entries { return makeK0WildcardEntries(n, 1000) }},
		{"sparse_stride1k", 100_000, func(n int) Entries { return makeK0WildcardEntries(n, 1000) }},
		{"sparse_stride1k", 1_000_000, func(n int) Entries { return makeK0WildcardEntries(n, 1000) }},
		{"multi_idx8", 100_000, func(n int) Entries { return makeK0WildcardEntriesMultiIndex(n, 8) }},
		{"multi_idx64", 100_000, func(n int) Entries { return makeK0WildcardEntriesMultiIndex(n, 64) }},
	}

	t.Logf("%-16s %10s %12s %14s %14s %10s %10s",
		"dist", "n", "slice_B", "rb_heap_B", "rb_ser_B", "ratio_h", "ratio_s")
	for _, r := range rows {
		e := r.make(r.n)
		bm := roaringFromEntries(e)
		ser, err := bm.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		sliceB := len(e) * 8
		heapB := bm.GetSizeInBytes()
		serB := len(ser)
		// also report GetSerializedSizeInBytes when available
		_ = bm.GetSerializedSizeInBytes()
		t.Logf("%-16s %10d %12d %14d %14d %10.3f %10.3f",
			r.label, r.n, sliceB, heapB, serB,
			float64(heapB)/float64(sliceB),
			float64(serB)/float64(sliceB),
		)
	}
}

// --- SkipTo benchmarks ---

type zlistFixture struct {
	entries Entries
	bm      *roaring64.Bitmap
}

func newZListFixture(n int, docStride int64) zlistFixture {
	e := makeK0WildcardEntries(n, docStride)
	return zlistFixture{entries: e, bm: roaringFromEntries(e)}
}

// seqTargets: SkipTo each successive entry (monotone full scan, merge-like).
func seqTargets(e Entries) []EntryID {
	// Skipping to e[i] from previous position visits every element once.
	return append(Entries(nil), e...)
}

// strideTargets: visit every `stride`-th entry (moderate jumps).
func strideTargets(e Entries, stride int) []EntryID {
	if stride < 1 {
		stride = 1
	}
	out := make(Entries, 0, (len(e)+stride-1)/stride)
	for i := 0; i < len(e); i += stride {
		out = append(out, e[i])
	}
	return out
}

// jumpTargets: log-spaced large jumps across the key space.
func jumpTargets(e Entries, jumps int) []EntryID {
	if len(e) == 0 || jumps < 1 {
		return nil
	}
	out := make(Entries, 0, jumps)
	for i := 0; i < jumps; i++ {
		// evenly spaced positions
		idx := i * (len(e) - 1) / max(jumps-1, 1)
		out = append(out, e[idx])
	}
	return out
}

// randomMonotoneTargets: random increasing SkipTo targets (merge with noisy peers).
func randomMonotoneTargets(e Entries, steps int, seed int64) []EntryID {
	if len(e) == 0 || steps < 1 {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	out := make(Entries, 0, steps)
	pos := 0
	for len(out) < steps && pos < len(e) {
		// advance by 1..maxStep
		maxStep := max(len(e)/steps, 1)
		pos += 1 + rng.Intn(maxStep)
		if pos >= len(e) {
			pos = len(e) - 1
			out = append(out, e[pos])
			break
		}
		out = append(out, e[pos])
	}
	return out
}

func runSkipToSeq(it interface {
	SkipTo(EntryID) EntryID
	Current() EntryID
	ReachEnd() bool
}, targets []EntryID) (sink EntryID) {
	for _, tgt := range targets {
		sink = it.SkipTo(tgt)
	}
	return sink
}

func BenchmarkWildcardSkipTo(b *testing.B) {
	type dist struct {
		name   string
		stride int64
	}
	dists := []dist{
		{"dense", 1},
		{"sparse1k", 1000},
	}
	sizes := []int{1_000, 10_000, 100_000, 1_000_000}

	type pattern struct {
		name    string
		targets func(e Entries) []EntryID
	}

	for _, d := range dists {
		for _, n := range sizes {
			fx := newZListFixture(n, d.stride)
			patterns := []pattern{
				{"seq", seqTargets},
				{"stride16", func(e Entries) []EntryID { return strideTargets(e, 16) }},
				{"stride256", func(e Entries) []EntryID { return strideTargets(e, 256) }},
				{"jump64", func(e Entries) []EntryID { return jumpTargets(e, 64) }},
				{"jump1k", func(e Entries) []EntryID { return jumpTargets(e, 1000) }},
				{"randMono1k", func(e Entries) []EntryID { return randomMonotoneTargets(e, 1000, 42) }},
			}
			for _, p := range patterns {
				targets := p.targets(fx.entries)
				// Skip degenerate patterns on tiny lists.
				if len(targets) == 0 {
					continue
				}

				b.Run(fmt.Sprintf("%s/n=%d/%s/slice", d.name, n, p.name), func(b *testing.B) {
					b.ReportAllocs()
					var sink EntryID
					for i := 0; i < b.N; i++ {
						it := NewSliceIterator(WildcardTerm, fx.entries)
						sink = runSkipToSeq(it, targets)
					}
					_ = sink
				})

				b.Run(fmt.Sprintf("%s/n=%d/%s/roaring", d.name, n, p.name), func(b *testing.B) {
					b.ReportAllocs()
					var sink EntryID
					for i := 0; i < b.N; i++ {
						it := newRoaringEntryIter(fx.bm)
						sink = runSkipToSeq(it, targets)
					}
					_ = sink
				})
			}
		}
	}
}

// BenchmarkWildcardSkipTo_ReuseIter measures advance cost without per-query
// iterator construction (lower bound of hot-path SkipTo).
func BenchmarkWildcardSkipTo_ReuseIter(b *testing.B) {
	for _, n := range []int{100_000, 1_000_000} {
		fx := newZListFixture(n, 1)
		targets := seqTargets(fx.entries)

		b.Run(fmt.Sprintf("n=%d/seq/slice", n), func(b *testing.B) {
			b.ReportAllocs()
			it := NewSliceIterator(WildcardTerm, fx.entries)
			var sink EntryID
			for i := 0; i < b.N; i++ {
				// reset by reconstructing — SliceIterator has no Reset;
				// approximate reuse by re-binding cursor via new struct on same slice.
				*it = *NewSliceIterator(WildcardTerm, fx.entries)
				sink = runSkipToSeq(it, targets)
			}
			_ = sink
		})

		b.Run(fmt.Sprintf("n=%d/seq/roaring", n), func(b *testing.B) {
			b.ReportAllocs()
			var sink EntryID
			for i := 0; i < b.N; i++ {
				it := newRoaringEntryIter(fx.bm)
				sink = runSkipToSeq(it, targets)
			}
			_ = sink
		})
	}
}

// BenchmarkWildcardBuild measures one-shot construction cost of each representation
// (relevant to open/load path, not per-query).
func BenchmarkWildcardBuild(b *testing.B) {
	for _, n := range []int{10_000, 100_000, 1_000_000} {
		e := makeK0WildcardEntries(n, 1)

		b.Run(fmt.Sprintf("n=%d/slice_copy", n), func(b *testing.B) {
			b.ReportAllocs()
			var sink int
			for i := 0; i < b.N; i++ {
				cp := append(Entries(nil), e...)
				sink = len(cp)
			}
			_ = sink
		})

		b.Run(fmt.Sprintf("n=%d/roaring_from_slice", n), func(b *testing.B) {
			b.ReportAllocs()
			var sink uint64
			for i := 0; i < b.N; i++ {
				bm := roaringFromEntries(e)
				sink = bm.GetCardinality()
			}
			_ = sink
		})

		bm := roaringFromEntries(e)
		ser, err := bm.MarshalBinary()
		if err != nil {
			b.Fatal(err)
		}

		b.Run(fmt.Sprintf("n=%d/roaring_unmarshal", n), func(b *testing.B) {
			b.ReportAllocs()
			var sink uint64
			for i := 0; i < b.N; i++ {
				rb := roaring64.New()
				if err := rb.UnmarshalBinary(ser); err != nil {
					b.Fatal(err)
				}
				sink = rb.GetCardinality()
			}
			_ = sink
		})
	}
}
