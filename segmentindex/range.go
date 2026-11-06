package segmentindex

import (
	"fmt"
	"math"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

// rangePredicate is one numeric range expression of a conjunction. Bounds
// describe the half-open interval [left, right), matching the semantics of
// holder/rangeholder (see ParseRange / Range.ContainValue): GT is strict,
// LT is strict and Between excludes its right bound.
type rangePredicate struct {
	field string
	left  int64
	right int64
	incl  bool // include range if true, exclude range if false
}

// contains reports whether value falls into the half-open interval [left, right).
func (r rangePredicate) contains(value int64) bool {
	return value >= r.left && value < r.right
}

// parsePredicateRange converts a non-EQ numeric operator (GT/LT/Between) and
// its value into half-open [left, right) bounds. The mapping mirrors
// rangeholder.ParseRange so both engines agree on query results:
//
//	GT v        -> [v+1, MaxInt64)  strictly greater than v
//	LT v        -> [MinInt64, v)    strictly less than v
//	Between l,h -> [l, h)           a degenerate l == h covers exactly l
//
// Values must be integers or integer strings; floats are rejected so no
// bound is silently truncated. Between additionally accepts [2]int64, two
// element slices, and "x:y" range descriptions.
func parsePredicateRange(opt be.ValueOpt, value be.Values) (int64, int64, error) {
	switch opt {
	case be.ValueOptGT:
		number, err := parser.ParseIntegerNumber(value, false)
		if err != nil {
			return 0, 0, fmt.Errorf("gt operator needs integer value:%v, err:%v", value, err)
		}
		if number == math.MaxInt64 {
			// nothing is greater than MaxInt64: empty interval
			return math.MaxInt64, math.MaxInt64, nil
		}
		return number + 1, math.MaxInt64, nil
	case be.ValueOptLT:
		number, err := parser.ParseIntegerNumber(value, false)
		if err != nil {
			return 0, 0, fmt.Errorf("lt operator needs integer value:%v, err:%v", value, err)
		}
		return math.MinInt64, number, nil
	case be.ValueOptBetween:
		var numbers []int64
		switch v := value.(type) {
		case [2]int64:
			numbers = []int64{v[0], v[1]}
		case string:
			rgDesc := parser.NewRangeDesc(v)
			if rgDesc == nil {
				return 0, 0, fmt.Errorf("not a valid range description, need x:y input:%s", v)
			}
			left, right, _ := rgDesc.Values()
			numbers = []int64{left, right}
		default:
			parsed, err := parser.ParseIntegers(value, false)
			if err != nil {
				return 0, 0, fmt.Errorf("between operator needs integer bounds:%v, err:%v", value, err)
			}
			numbers = parsed
		}
		if len(numbers) != 2 {
			return 0, 0, fmt.Errorf("between operator needs two bounds, input:%v", value)
		}
		left, right := numbers[0], numbers[1]
		if left > right {
			return 0, 0, fmt.Errorf("%d > %d, bad range", left, right)
		}
		if left == right {
			if left == math.MaxInt64 {
				return 0, 0, fmt.Errorf("bad range:[%d,%d)", left, right)
			}
			right = left + 1
		}
		return left, right, nil
	}
	return 0, 0, fmt.Errorf("unsupported range operator:%d", opt)
}

// evaluateFieldRanges checks a conjunction's numeric range predicates for one
// field against the assigned values. Assigned values that are not parseable
// as integers never satisfy a range. It reports whether any include range
// was hit, whether any exclude range was hit, and whether the field carries
// include range constraints at all (needed for the wildcard fallback).
func evaluateFieldRanges(ranges []rangePredicate, field string, values be.Values) (inclHit, exclHit, hasIncl bool) {
	var fieldRanges []rangePredicate
	for _, r := range ranges {
		if r.field == field {
			fieldRanges = append(fieldRanges, r)
		}
	}
	if len(fieldRanges) == 0 {
		return false, false, false
	}
	// non-numeric assigned values simply never fall into a range
	numbers, _ := parser.ParseIntegers(values, false)
	for _, r := range fieldRanges {
		if r.incl {
			hasIncl = true
		}
		for _, number := range numbers {
			if r.contains(number) {
				if r.incl {
					inclHit = true
				} else {
					exclHit = true
				}
				break
			}
		}
	}
	return inclHit, exclHit, hasIncl
}
