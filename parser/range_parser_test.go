package parser

import (
	"math"
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestParseRangeExpr(t *testing.T) {
	cases := []struct {
		name string
		op   core.ValueOpt
		val  interface{}
		want []IntInterval
		err  bool
	}{
		{"eq single", core.ValueOptEQ, 5, []IntInterval{{5, 5}}, false},
		{"eq multi", core.ValueOptEQ, []int{1, 2, 3}, []IntInterval{{1, 1}, {2, 2}, {3, 3}}, false},
		{"gt", core.ValueOptGT, 18, []IntInterval{{19, math.MaxInt64}}, false},
		{"gt max", core.ValueOptGT, int64(math.MaxInt64), nil, false},
		{"lt", core.ValueOptLT, 22, []IntInterval{{math.MinInt64, 21}}, false},
		{"lt min", core.ValueOptLT, int64(math.MinInt64), nil, false},
		{"between", core.ValueOptBetween, []int64{10, 20}, []IntInterval{{10, 20}}, false},
		{"between single point", core.ValueOptBetween, []int64{7, 7}, []IntInterval{{7, 7}}, false},
		{"between out of order", core.ValueOptBetween, []int64{20, 10}, nil, true},
		{"between wrong arity", core.ValueOptBetween, []int64{1, 2, 3}, nil, true},
		{"gt wrong arity", core.ValueOptGT, []int64{1, 2}, nil, true},
		{"bad op", core.ValueOpt(99), 1, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseRangeExpr(c.op, c.val)
			if c.err {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestParseRangePoint(t *testing.T) {
	p, err := ParseRangePoint(25)
	if err != nil || p != 25 {
		t.Fatalf("got %d err %v", p, err)
	}
	if _, err := ParseRangePoint([]int{1, 2}); err == nil {
		t.Fatal("expected error for multi-value point")
	}
}
