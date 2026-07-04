package parser

import (
	"fmt"
	"math"

	"github.com/echoface/be_indexer/core"
)

// IntInterval is a closed integer interval [Lo, Hi].
type IntInterval struct {
	Lo int64
	Hi int64
}

// ParseRangeExpr converts a boolean range predicate (operator + value) into one
// or more closed integer intervals that the ext_range segment-tree container
// indexes. It is the build-side counterpart that finally honours
// core.ValueExpr.Operator, which the EQ-only tokenizer path ignores.
//
// Semantics (all clamped to the int64 domain):
//
//	EQ      v / [a,b,...]  -> one [x,x] interval per distinct value
//	GT      v              -> [v+1, MaxInt64]   (strictly greater)
//	LT      v              -> [MinInt64, v-1]   (strictly less)
//	Between [lo, hi]       -> [lo, hi]
//
// GT(MaxInt64) and LT(MinInt64) yield the empty set (no interval), which is the
// correct degenerate semantics (nothing is strictly greater than the max).
func ParseRangeExpr(op core.ValueOpt, value interface{}) ([]IntInterval, error) {
	switch op {
	case core.ValueOptEQ:
		vals, err := ParseIntegers(value, true)
		if err != nil {
			return nil, err
		}
		intervals := make([]IntInterval, 0, len(vals))
		for _, v := range vals {
			intervals = append(intervals, IntInterval{Lo: v, Hi: v})
		}
		return intervals, nil

	case core.ValueOptGT:
		v, err := singleInt(value)
		if err != nil {
			return nil, err
		}
		if v == math.MaxInt64 {
			return nil, nil // nothing is strictly greater than MaxInt64
		}
		return []IntInterval{{Lo: v + 1, Hi: math.MaxInt64}}, nil

	case core.ValueOptLT:
		v, err := singleInt(value)
		if err != nil {
			return nil, err
		}
		if v == math.MinInt64 {
			return nil, nil // nothing is strictly less than MinInt64
		}
		return []IntInterval{{Lo: math.MinInt64, Hi: v - 1}}, nil

	case core.ValueOptBetween:
		lo, hi, err := pairInt(value)
		if err != nil {
			return nil, err
		}
		if lo > hi {
			return nil, fmt.Errorf("between bounds out of order: [%d,%d]", lo, hi)
		}
		return []IntInterval{{Lo: lo, Hi: hi}}, nil

	default:
		return nil, fmt.Errorf("unsupported range operator: %d", op)
	}
}

// singleInt extracts exactly one int64 from a scalar or single-element slice.
func singleInt(value interface{}) (int64, error) {
	vals, err := ParseIntegers(value, true)
	if err != nil {
		return 0, err
	}
	if len(vals) != 1 {
		return 0, fmt.Errorf("expected a single value, got %d", len(vals))
	}
	return vals[0], nil
}

// pairInt extracts exactly two int64 (lo, hi) for between.
func pairInt(value interface{}) (int64, int64, error) {
	vals, err := ParseIntegers(value, true)
	if err != nil {
		return 0, 0, err
	}
	if len(vals) != 2 {
		return 0, 0, fmt.Errorf("between expects two values, got %d", len(vals))
	}
	return vals[0], vals[1], nil
}

// ParseRangePoint parses a query assignment value into a single int64 point for
// stabbing the ext_range container.
func ParseRangePoint(value interface{}) (int64, error) {
	return singleInt(value)
}
