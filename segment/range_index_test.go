package segment

import (
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
