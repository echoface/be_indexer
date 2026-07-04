package parser

import (
	"encoding/json"
	"fmt"

	"github.com/echoface/be_indexer/util"
)

// NumberParser tokenizes numeric values into string terms.
type NumberParser struct {
	floatAsInt bool
}

// NewNumberParser creates a NumberParser with float→int enabled.
func NewNumberParser() *NumberParser {
	return &NumberParser{floatAsInt: true}
}

// NewNumberParser2 creates a NumberParser with explicit floatAsInt control.
func NewNumberParser2(f2i bool) *NumberParser {
	return &NumberParser{floatAsInt: f2i}
}

// TokenizeAssign implements ValueTokenizer for query phase.
func (p *NumberParser) TokenizeAssign(v interface{}) ([]string, error) {
	ids, err := p.parseValues(v, false)
	if err != nil {
		return nil, err
	}
	results := make([]string, len(ids))
	for i, id := range ids {
		results[i] = fmt.Sprintf("%d", id)
	}
	return results, nil
}

// TokenizeValue implements ValueTokenizer for indexing phase.
func (p *NumberParser) TokenizeValue(v interface{}) ([]string, error) {
	ids, err := p.parseValues(v, true)
	if err != nil {
		return nil, err
	}
	results := make([]string, len(ids))
	for i, id := range ids {
		results[i] = fmt.Sprintf("%d", id)
	}
	return results, nil
}

// parseValues converts a value or slice of values to int64 IDs.
// allocMissing controls whether unknown values get allocated (true for indexing, false for query).
func (p *NumberParser) parseValues(v interface{}, allocMissing bool) ([]int64, error) {
	if util.NilInterface(v) {
		return nil, nil
	}

	toInt := func(iv interface{}) (int64, error) {
		switch n := iv.(type) {
		case int:
			return int64(n), nil
		case int8:
			return int64(n), nil
		case int16:
			return int64(n), nil
		case int32:
			return int64(n), nil
		case int64:
			return n, nil
		case uint:
			return int64(n), nil
		case uint8:
			return int64(n), nil
		case uint16:
			return int64(n), nil
		case uint32:
			return int64(n), nil
		case uint64:
			return int64(n), nil
		case float32:
			if p.floatAsInt {
				return int64(n), nil
			}
			return 0, fmt.Errorf("float not supported: %v", iv)
		case float64:
			if p.floatAsInt {
				return int64(n), nil
			}
			return 0, fmt.Errorf("float not supported: %v", iv)
		case json.Number:
			s := string(n)
			ni, err := ParseIntegerNumber(s, p.floatAsInt)
			if err != nil {
				return 0, err
			}
			return ni, nil
		default:
			ni, err := ParseIntegerNumber(iv, p.floatAsInt)
			if err != nil {
				return 0, err
			}
			return ni, nil
		}
	}

	switch t := v.(type) {
	case []int64:
		return t, nil
	case []int32:
		res := make([]int64, len(t))
		for i, x := range t {
			res[i] = int64(x)
		}
		return res, nil
	case []int:
		res := make([]int64, len(t))
		for i, x := range t {
			res[i] = int64(x)
		}
		return res, nil
	case []float64:
		if !p.floatAsInt {
			return nil, fmt.Errorf("[]float not supported: %v", v)
		}
		res := make([]int64, len(t))
		for i, x := range t {
			res[i] = int64(x)
		}
		return res, nil
	case []interface{}:
		res := make([]int64, len(t))
		for i, iv := range t {
			n, err := toInt(iv)
			if err != nil {
				return nil, err
			}
			res[i] = n
		}
		return res, nil
	}

	// Single value
	n, err := toInt(v)
	if err != nil {
		return nil, err
	}
	return []int64{n}, nil
}
