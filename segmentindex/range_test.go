package segmentindex

import (
	"math"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/holder/rangeholder"
)

func TestParsePredicateRange(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opt       be.ValueOpt
		value     be.Values
		wantLeft  int64
		wantRight int64
		wantErr   bool
	}{
		{"gt", be.ValueOptGT, int64(18), 19, math.MaxInt64, false},
		{"gt numeric string", be.ValueOptGT, "18", 19, math.MaxInt64, false},
		{"gt max int is empty", be.ValueOptGT, int64(math.MaxInt64), math.MaxInt64, math.MaxInt64, false},
		{"gt needs integer", be.ValueOptGT, "abc", 0, 0, true},
		{"gt rejects float", be.ValueOptGT, 3.5, 0, 0, true},
		{"lt", be.ValueOptLT, int64(20), math.MinInt64, 20, false},
		{"lt min int is empty", be.ValueOptLT, int64(math.MinInt64), math.MinInt64, math.MinInt64, false},
		{"lt needs integer", be.ValueOptLT, []int64{1}, 0, 0, true},
		{"between", be.ValueOptBetween, []int64{0, 25}, 0, 25, false},
		{"between array", be.ValueOptBetween, [2]int64{5, 5}, 5, 6, false},
		{"between range desc", be.ValueOptBetween, "0:25", 0, 25, false},
		{"between numeric strings", be.ValueOptBetween, []string{"3", "7"}, 3, 7, false},
		{"between rejects reversed", be.ValueOptBetween, []int64{25, 0}, 0, 0, true},
		{"between needs two", be.ValueOptBetween, []int64{1}, 0, 0, true},
		{"between bad desc", be.ValueOptBetween, "x", 0, 0, true},
		{"between max int is unrepresentable", be.ValueOptBetween, [2]int64{math.MaxInt64, math.MaxInt64}, 0, 0, true},
		{"eq is not a range", be.ValueOptEQ, int64(1), 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, right, err := parsePredicateRange(tc.opt, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got [%d,%d), want error", left, right)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if left != tc.wantLeft || right != tc.wantRight {
				t.Fatalf("got [%d,%d) want [%d,%d)", left, right, tc.wantLeft, tc.wantRight)
			}
		})
	}
}

func TestRangePredicateContains(t *testing.T) {
	pred := rangePredicate{left: 0, right: 25}
	for probe, want := range map[int64]bool{
		-1:  false,
		0:   true,
		24:  true,
		25:  false,
		100: false,
	} {
		if got := pred.contains(probe); got != want {
			t.Fatalf("probe=%d contains=%v want %v", probe, got, want)
		}
	}
	// GT(MaxInt64) and LT(MinInt64) describe empty value spaces
	for _, tc := range []struct {
		opt   be.ValueOpt
		value be.Values
	}{
		{be.ValueOptGT, int64(math.MaxInt64)},
		{be.ValueOptLT, int64(math.MinInt64)},
	} {
		left, right, err := parsePredicateRange(tc.opt, tc.value)
		if err != nil {
			t.Fatal(err)
		}
		pred := rangePredicate{left: left, right: right}
		if pred.contains(left) {
			t.Fatalf("opt=%d range [%d,%d) must be empty", tc.opt, left, right)
		}
	}
}

// TestParsePredicateRangeMatchesRangeHolder pins the parsed bounds to the
// canonical holder/rangeholder semantics: both engines must answer query
// values identically. Boundary corners where rangeholder overflows
// (GT(MaxInt64)) or widens a degenerate range (LT(MinInt64)) are excluded
// here and pinned by TestRangePredicateContains instead.
func TestParsePredicateRangeMatchesRangeHolder(t *testing.T) {
	probes := []int64{
		math.MinInt64, math.MinInt64 + 1, -10, -1, 0, 1, 5, 6,
		17, 18, 19, 20, 24, 25, 26, 100, math.MaxInt64 - 1, math.MaxInt64,
	}
	for _, tc := range []struct {
		name  string
		opt   be.ValueOpt
		value be.Values
	}{
		{"gt", be.ValueOptGT, int64(18)},
		{"gt numeric string", be.ValueOptGT, "18"},
		{"lt", be.ValueOptLT, int64(20)},
		{"between slice", be.ValueOptBetween, []int64{0, 25}},
		{"between degenerate", be.ValueOptBetween, [2]int64{5, 5}},
		{"between range desc", be.ValueOptBetween, "0:25"},
		{"between negatives", be.ValueOptBetween, []int64{-10, -5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, right, err := parsePredicateRange(tc.opt, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			holderRange, err := rangeholder.ParseRange(tc.opt, tc.value, false)
			if err != nil {
				t.Fatal(err)
			}
			pred := rangePredicate{left: left, right: right}
			for _, probe := range probes {
				if got, want := pred.contains(probe), holderRange.ContainValue(probe); got != want {
					t.Fatalf("probe=%d: contains=%v, rangeholder ContainValue=%v, bounds [%d,%d) vs %s",
						probe, got, want, left, right, holderRange)
				}
			}
		})
	}
}

func TestEvaluateFieldRanges(t *testing.T) {
	ranges := []rangePredicate{
		{field: "age", left: 19, right: math.MaxInt64, incl: true},
		{field: "age", left: 0, right: 25, incl: false},
		{field: "city", left: 0, right: 1, incl: true},
	}
	for _, tc := range []struct {
		name    string
		field   string
		values  be.Values
		inclHit bool
		exclHit bool
		hasIncl bool
	}{
		{"both hit", "age", be.NewIntValues(20), true, true, true},
		{"include only", "age", be.NewIntValues(26), true, false, true},
		{"exclude only", "age", be.NewIntValues(18), false, true, true},
		{"no hit", "age", be.NewIntValues(-1), false, false, true},
		{"multi values", "age", be.NewInt64Values(18, 30), true, true, true},
		{"non numeric values never hit", "age", be.NewStrValues("abc"), false, false, true},
		{"field without ranges", "unknown", be.NewIntValues(20), false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inclHit, exclHit, hasIncl := evaluateFieldRanges(ranges, tc.field, tc.values)
			if inclHit != tc.inclHit || exclHit != tc.exclHit || hasIncl != tc.hasIncl {
				t.Fatalf("got incl=%v excl=%v hasIncl=%v, want incl=%v excl=%v hasIncl=%v",
					inclHit, exclHit, hasIncl, tc.inclHit, tc.exclHit, tc.hasIncl)
			}
		})
	}
}
