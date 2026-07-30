package segment

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
)

// bruteStab returns the set of EntryIDs whose interval contains q, the ground
// truth to validate the segment tree against.
func bruteStab(intervals []Interval, q int64) []core.EntryID {
	var out []core.EntryID
	for _, iv := range intervals {
		if iv.Lo <= q && q <= iv.Hi {
			out = append(out, iv.Entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func stabResult(t *testing.T, ri *RangeIndex, q int64) []core.EntryID {
	t.Helper()
	iters, err := ri.Stab("f", q)
	if err != nil {
		t.Fatalf("stab %d: %v", q, err)
	}
	var got []core.EntryID
	for _, it := range iters {
		for e := it.Current(); !e.IsNULLEntry(); {
			got = append(got, e)
			e = it.SkipTo(e + 1)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	return got
}

func eqEntries(a, b []core.EntryID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func buildRI(t *testing.T, intervals []Interval) *RangeIndex {
	t.Helper()
	raw, err := BuildRangeIndex(intervals)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	ri, err := NewRangeReader(raw)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return ri
}

func TestRangeIndexBasic(t *testing.T) {
	intervals := []Interval{
		{Lo: 18, Hi: math.MaxInt64, Entry: 100}, // age > 17  (>=18)
		{Lo: math.MinInt64, Hi: 21, Entry: 200}, // age < 22  (<=21)
		{Lo: 18, Hi: 25, Entry: 300},            // between [18,25]
		{Lo: 30, Hi: 30, Entry: 400},            // exactly 30
	}
	ri := buildRI(t, intervals)

	cases := map[int64][]core.EntryID{
		17:  {200},           // <22 only
		18:  {100, 200, 300}, // >=18, <22, [18,25]
		25:  {100, 300},      // >=18, [18,25]
		30:  {100, 400},      // >=18, exactly 30
		100: {100},           // only >=18
		math.MinInt64: {200},
		math.MaxInt64: {100},
	}
	for q, want := range cases {
		got := stabResult(t, ri, q)
		bw := bruteStab(intervals, q)
		if !eqEntries(got, bw) {
			t.Fatalf("q=%d sanity: brute %v != case %v", q, bw, want)
		}
		if !eqEntries(got, want) {
			t.Fatalf("q=%d: got %v want %v", q, got, want)
		}
	}
}

func TestRangeIndexEmpty(t *testing.T) {
	ri := buildRI(t, nil)
	if got := stabResult(t, ri, 5); len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestRangeIndexSinglePoint(t *testing.T) {
	ri := buildRI(t, []Interval{{Lo: 7, Hi: 7, Entry: 9}})
	if got := stabResult(t, ri, 7); !eqEntries(got, []core.EntryID{9}) {
		t.Fatalf("q=7 got %v", got)
	}
	if got := stabResult(t, ri, 6); len(got) != 0 {
		t.Fatalf("q=6 got %v", got)
	}
	if got := stabResult(t, ri, 8); len(got) != 0 {
		t.Fatalf("q=8 got %v", got)
	}
}

func TestRangeIndexNestedOverlap(t *testing.T) {
	intervals := []Interval{
		{Lo: 0, Hi: 100, Entry: 1},
		{Lo: 10, Hi: 90, Entry: 2},
		{Lo: 20, Hi: 80, Entry: 3},
		{Lo: 50, Hi: 50, Entry: 4},
	}
	ri := buildRI(t, intervals)
	for _, q := range []int64{-1, 0, 5, 10, 20, 50, 80, 90, 100, 101} {
		got := stabResult(t, ri, q)
		want := bruteStab(intervals, q)
		if !eqEntries(got, want) {
			t.Fatalf("q=%d got %v want %v", q, got, want)
		}
	}
}

func TestRangeIndexInvalidInterval(t *testing.T) {
	if _, err := BuildRangeIndex([]Interval{{Lo: 5, Hi: 4, Entry: 1}}); err == nil {
		t.Fatal("expected error for lo>hi")
	}
}

func TestRangeIndexFuzzAgainstBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	for iter := 0; iter < 200; iter++ {
		n := rng.Intn(40) + 1
		intervals := make([]Interval, 0, n)
		for i := 0; i < n; i++ {
			a := int64(rng.Intn(60) - 30)
			b := int64(rng.Intn(60) - 30)
			if a > b {
				a, b = b, a
			}
			// occasionally use unbounded edges
			switch rng.Intn(5) {
			case 0:
				a = math.MinInt64
			case 1:
				b = math.MaxInt64
			}
			intervals = append(intervals, Interval{Lo: a, Hi: b, Entry: core.EntryID(i + 1)})
		}
		ri := buildRI(t, intervals)
		for q := int64(-35); q <= 35; q++ {
			got := stabResult(t, ri, q)
			want := bruteStab(intervals, q)
			if !eqEntries(got, want) {
				t.Fatalf("iter=%d q=%d got %v want %v\nintervals=%v", iter, q, got, want, intervals)
			}
		}
		// also probe the extreme points
		for _, q := range []int64{math.MinInt64, math.MaxInt64} {
			got := stabResult(t, ri, q)
			want := bruteStab(intervals, q)
			if !eqEntries(got, want) {
				t.Fatalf("iter=%d q=%d(extreme) got %v want %v", iter, q, got, want)
			}
		}
	}
}

func TestRangeIndexReaderTruncated(t *testing.T) {
	raw, err := BuildRangeIndex([]Interval{{Lo: 1, Hi: 2, Entry: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRangeReader(raw[:10]); err == nil {
		t.Fatal("expected truncated error")
	}
	bad := append([]byte(nil), raw...)
	bad[0] = 'X'
	if _, err := NewRangeReader(bad); err == nil {
		t.Fatal("expected magic error")
	}
}

// randomIntervals builds n bounded uniform intervals for benchmarking.
func randomIntervals(rng *rand.Rand, n int) []Interval {
	intervals := make([]Interval, 0, n)
	for i := 0; i < n; i++ {
		lo := int64(rng.Intn(1000000))
		hi := lo + int64(rng.Intn(100))
		intervals = append(intervals, Interval{Lo: lo, Hi: hi, Entry: core.EntryID(i)})
	}
	return intervals
}

// TestRangeIndexPointRouting verifies equality (Lo==Hi) intervals land in the
// packed point index and range intervals land in the segment tree, and that
// both are stabbed correctly and unioned.
func TestRangeIndexPointRouting(t *testing.T) {
	intervals := []Interval{
		{Lo: 10, Hi: 10, Entry: 1},   // point
		{Lo: 20, Hi: 20, Entry: 2},   // point
		{Lo: 20, Hi: 20, Entry: 3},   // point, same key different entry
		{Lo: 5, Hi: 30, Entry: 4},    // range covering both points
		{Lo: 100, Hi: 200, Entry: 5}, // disjoint range
	}
	ri := buildRI(t, intervals)

	if got := int(ri.nodeCount); got == 0 {
		t.Fatal("expected a segment tree for range intervals")
	}
	// Only the two distinct range endpoints' values feed the tree, so point keys
	// are tracked separately from the tree.
	if len(ri.pointKeys) != 2 { // keys 10 and 20
		t.Fatalf("expected 2 point keys, got %v", ri.pointKeys)
	}

	for _, q := range []int64{4, 5, 10, 15, 20, 30, 31, 99, 100, 150, 200, 201} {
		got := stabResult(t, ri, q)
		want := bruteStab(intervals, q)
		if !eqEntries(got, want) {
			t.Fatalf("q=%d got %v want %v", q, got, want)
		}
	}
}

// TestRangeIndexPointsOnly verifies a pure-equality workload builds with no
// segment tree at all (nodeCount==0), the intended win for numeric fields
// dominated by = / in predicates.
func TestRangeIndexPointsOnly(t *testing.T) {
	intervals := []Interval{
		{Lo: 3, Hi: 3, Entry: 1},
		{Lo: 7, Hi: 7, Entry: 2},
		{Lo: 3, Hi: 3, Entry: 3},
		{Lo: 100, Hi: 100, Entry: 4},
	}
	ri := buildRI(t, intervals)
	if ri.nodeCount != 0 {
		t.Fatalf("pure point workload should build no tree, got nodeCount=%d", ri.nodeCount)
	}
	if len(ri.pointKeys) != 3 { // 3, 7, 100
		t.Fatalf("expected 3 distinct point keys, got %v", ri.pointKeys)
	}
	for _, q := range []int64{2, 3, 4, 7, 100, 101} {
		got := stabResult(t, ri, q)
		want := bruteStab(intervals, q)
		if !eqEntries(got, want) {
			t.Fatalf("q=%d got %v want %v", q, got, want)
		}
	}
}

// TestRangeIndexTreeSizeTracksRangesNotPoints is the core space guarantee: the
// segment tree's node count is governed by the number of *range* endpoints, not
// the field's equality-value cardinality. Adding many distinct EQ values must
// not grow the tree.
func TestRangeIndexTreeSizeTracksRangesNotPoints(t *testing.T) {
	// Baseline: a single range, no points.
	base := buildRI(t, []Interval{{Lo: 0, Hi: 1000, Entry: 1}})

	// Same single range plus 10k distinct equality points.
	intervals := []Interval{{Lo: 0, Hi: 1000, Entry: 1}}
	for i := 0; i < 10000; i++ {
		intervals = append(intervals, Interval{Lo: int64(1_000_000 + i), Hi: int64(1_000_000 + i), Entry: core.EntryID(i + 2)})
	}
	withPoints := buildRI(t, intervals)

	if withPoints.nodeCount != base.nodeCount {
		t.Fatalf("tree grew with equality points: base=%d withPoints=%d", base.nodeCount, withPoints.nodeCount)
	}
	if len(withPoints.pointKeys) != 10000 {
		t.Fatalf("expected 10000 point keys, got %d", len(withPoints.pointKeys))
	}
}

// TestRangeIndexZeroAllocQuery verifies the hot query path performs no heap
// allocation for a point hit (the FlatPostingList cursor is a value the caller
// ranges over; Stab itself must not allocate for the point-index probe).
func TestRangeIndexZeroAllocQuery(t *testing.T) {
	intervals := make([]Interval, 0, 1000)
	for i := 0; i < 1000; i++ {
		v := int64(i * 2)
		intervals = append(intervals, Interval{Lo: v, Hi: v, Entry: core.EntryID(i + 1)})
	}
	ri := buildRI(t, intervals)

	// Probe a value that is NOT a key: findPoint returns 0, no cursor allocation,
	// no slice growth — the tightest zero-alloc guarantee for the point index.
	allocs := testing.AllocsPerRun(200, func() {
		_ = ri.findPoint(9999)
	})
	if allocs != 0 {
		t.Fatalf("findPoint miss should be zero-alloc, got %f", allocs)
	}
}

// BenchmarkBuildRangeIndex reports build-time allocations, the metric the flat
// (node, eid) pair accumulation was designed to reduce versus the former
// per-node []EntryID slices.
func BenchmarkBuildRangeIndex(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			intervals := randomIntervals(rng, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := BuildRangeIndex(intervals); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRangeIndexQuery(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			data, err := BuildRangeIndex(randomIntervals(rng, n))
			if err != nil {
				b.Fatal(err)
			}
			reader, err := NewRangeReader(data)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reader.MatchQuery(BlockContext{}, "test", int64(rng.Intn(1000000)))
			}
		})
	}
}

// pointHeavyIntervals models the target numeric workload: mostly high-cardinality
// equality predicates (Lo==Hi) with a small fraction of true ranges.
func pointHeavyIntervals(rng *rand.Rand, n int) []Interval {
	intervals := make([]Interval, 0, n)
	for i := 0; i < n; i++ {
		v := int64(rng.Intn(1000000))
		if rng.Intn(10) == 0 { // ~10% true ranges
			intervals = append(intervals, Interval{Lo: v, Hi: v + int64(rng.Intn(100)) + 1, Entry: core.EntryID(i)})
		} else { // ~90% equality points
			intervals = append(intervals, Interval{Lo: v, Hi: v, Entry: core.EntryID(i)})
		}
	}
	return intervals
}

// collectRangeBlock drives a RangeBuilder over the given records with the given
// spill env and returns the serialized RangeIndex block it produces.
func collectRangeBlock(t *testing.T, env BuilderEnv, intervals []Interval) []byte {
	t.Helper()
	b := NewRangeBuilder(env)
	for _, iv := range intervals {
		if err := b.AddRecord(core.RangeRecord{Lo: iv.Lo, Hi: iv.Hi}, []core.EntryID{iv.Entry}); err != nil {
			t.Fatalf("AddRecord: %v", err)
		}
	}
	var cw captureBlockWriter
	if err := b.Build(&cw); err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cw.data
}

// captureBlockWriter records the single block written by a builder.
type captureBlockWriter struct{ data []byte }

func (w *captureBlockWriter) WriteBlock(kind string, data []byte) error {
	w.data = append([]byte(nil), data...)
	return nil
}

// TestRangeBuilderSpillParity verifies that the RangeBuilder produces
// query-identical output whether it holds everything in memory or spills sorted
// runs to disk (MaxPostingsInMemory small enough to force multiple flushes).
// This guards the KeyedPostingCollector routing added for large-field builds.
func TestRangeBuilderSpillParity(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	intervals := pointHeavyIntervals(rng, 5000)

	inMem := collectRangeBlock(t, BuilderEnv{MaxPostingsInMemory: 0, TmpDir: ""}, intervals)
	spilled := collectRangeBlock(t, BuilderEnv{MaxPostingsInMemory: 128, TmpDir: t.TempDir()}, intervals)

	riMem, err := NewRangeReader(inMem)
	if err != nil {
		t.Fatalf("reader(in-mem): %v", err)
	}
	riSpill, err := NewRangeReader(spilled)
	if err != nil {
		t.Fatalf("reader(spilled): %v", err)
	}

	// Both must agree with brute force across a broad query sweep.
	for q := int64(-10); q < 1000010; q += 137 {
		want := bruteStab(intervals, q)
		if got := stabResult(t, riMem, q); !equalEntryIDs(got, want) {
			t.Fatalf("in-mem stab %d: got %v want %v", q, got, want)
		}
		if got := stabResult(t, riSpill, q); !equalEntryIDs(got, want) {
			t.Fatalf("spilled stab %d: got %v want %v", q, got, want)
		}
	}
}

func equalEntryIDs(a, b []core.EntryID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRangeBuilderRejectsInvalidInterval ensures Lo>Hi is rejected at AddRecord.
func TestRangeBuilderRejectsInvalidInterval(t *testing.T) {
	b := NewRangeBuilder(BuilderEnv{})
	if err := b.AddRecord(core.RangeRecord{Lo: 5, Hi: 4}, []core.EntryID{1}); err == nil {
		t.Fatal("expected error for Lo>Hi interval")
	}
}

// TestIntervalKeyRoundTripOrder verifies the 16-byte collector key both
// round-trips and preserves signed (lo,hi) ordering under bytewise compare,
// which spilled-run sorting relies on.
func TestIntervalKeyRoundTripOrder(t *testing.T) {
	vals := []int64{math.MinInt64, -1000, -1, 0, 1, 1000, math.MaxInt64}
	var prev []byte
	for _, lo := range vals {
		for _, hi := range vals {
			if lo > hi {
				continue
			}
			k := encodeIntervalKey(lo, hi)
			gl, gh := decodeIntervalKey(k)
			if gl != lo || gh != hi {
				t.Fatalf("roundtrip [%d,%d] -> [%d,%d]", lo, hi, gl, gh)
			}
			if prev != nil && bytesCompare(prev, k) >= 0 {
				t.Fatalf("key order violated at [%d,%d]", lo, hi)
			}
			prev = k
		}
	}
}

func bytesCompare(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

// BenchmarkBuildRangeIndexPointHeavy reports build allocations and serialized
// size for the point-dominated workload. The hybrid split keeps the segment
// tree small (only ~10% range endpoints enter it), so both build memory and
// file size drop versus routing every equality point through the tree.
func BenchmarkBuildRangeIndexPointHeavy(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(1))
			intervals := pointHeavyIntervals(rng, n)
			data, _ := BuildRangeIndex(intervals)
			ri, _ := NewRangeReader(data)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := BuildRangeIndex(intervals); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(data)), "bytes")
			b.ReportMetric(float64(ri.nodeCount), "treeNodes")
			b.ReportMetric(float64(len(ri.pointKeys)), "pointKeys")
		})
	}
}
