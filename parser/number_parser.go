package parser

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/echoface/be_indexer/util"
)

type (
	NumberParser struct {
		floatAsInt bool
	}
)

func NewNumberParser() *NumberParser {
	return &NumberParser{
		floatAsInt: true,
	}
}

func NewNumberParser2(f2i bool) *NumberParser {
	return &NumberParser{
		floatAsInt: true,
	}
}

func (p *NumberParser) Name() string {
	return "number"
}

// TokenizeAssign implements ValueTokenizer for query phase
func (p *NumberParser) TokenizeAssign(v interface{}) ([]string, error) {
	ids, err := p.ParseAssign(v)
	if err != nil {
		return nil, err
	}
	results := make([]string, len(ids))
	for i, id := range ids {
		results[i] = fmt.Sprintf("%d", id)
	}
	return results, nil
}

// TokenizeValue implements ValueTokenizer for indexing phase
func (p *NumberParser) TokenizeValue(v interface{}) ([]string, error) {
	ids, err := p.ParseValue(v)
	if err != nil {
		return nil, err
	}
	results := make([]string, len(ids))
	for i, id := range ids {
		results[i] = fmt.Sprintf("%d", id)
	}
	return results, nil
}

func (p *NumberParser) ParseAssign(v interface{}) (values []uint64, e error) {
	if util.NilInterface(v) {
		return values, nil
	}
	return p.ParseValue(v)
}

func (p *NumberParser) ParseValue(v interface{}) ([]uint64, error) {
	switch t := v.(type) {
	case string, json.Number, int, int8, int16, int32,
		int64, uint, uint8, uint16, uint32, uint64, float64, float32:
		if num, err := ParseIntegerNumber(t, p.floatAsInt); err == nil {
			return []uint64{uint64(num)}, nil
		}
	case []int8, []int16, []int32, []int, []int64, []uint8, []uint16,
		[]uint32, []uint, []uint64, []float32, []float64, []json.Number, []string:

		rv := reflect.ValueOf(t)
		res := make([]uint64, 0, rv.Len())
		var err error
		var num int64
		for i := 0; i < rv.Len(); i++ {
			vi := rv.Index(i).Interface()
			if num, err = ParseIntegerNumber(vi, p.floatAsInt); err != nil {
				return nil, err
			}
			res = append(res, uint64(num))
		}
		return res, nil
	case []interface{}:
		res := make([]uint64, 0, len(t))
		for _, iv := range t {
			if num, err := ParseIntegerNumber(iv, p.floatAsInt); err != nil {
				return nil, fmt.Errorf("value:%v not a interger-able value", iv)
			} else {
				res = append(res, uint64(num))
			}
		}
		return res, nil
	default:
		break
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}
