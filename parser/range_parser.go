package parser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/echoface/be_indexer/util"
)

var ErrBadRangeValue = fmt.Errorf("NumberRangeParser need:'start:end:step'")

/*
NumberRangeParser parse syntax like format: start:end:step
step ist optional, it will generate start, start+step, start+2*stem ....
*/
type (
	// NumberRangeParser format: start:end:step; the `step` is optional
	NumberRangeParser struct{}

	RangeDesc struct {
		start int64
		end   int64
		step  int64
	}
)

func NewNumRangeParser() ValueIDGenerator {
	return &NumberRangeParser{}
}

func NewRangeDesc(v string) *RangeDesc {
	opt := RangeDesc{step: 1}
	vs := strings.Split(v, ":")
	if len(vs) < 2 { // min len is 4 bz fmt:"x:x"
		return nil
	}
	var err error
	if opt.end, err = strconv.ParseInt(vs[1], 10, 64); err != nil {
		return nil
	}
	if opt.start, err = strconv.ParseInt(vs[0], 10, 64); err != nil {
		return nil
	}
	if len(vs) <= 2 {
		return &opt
	}
	if opt.step, err = strconv.ParseInt(vs[2], 10, 64); err != nil {
		return nil
	}
	return &opt
}

func (rgd *RangeDesc) Values() (start, end, stepLen int64) {
	return rgd.start, rgd.end, rgd.start
}

func (p *NumberRangeParser) Name() string {
	return "number_range"
}

// ParseAssign only single number supported, float will round into integer
func (p *NumberRangeParser) ParseAssign(v interface{}) (res []uint64, err error) {
	if util.NilInterface(v) {
		return res, err
	}

	switch val := v.(type) {
	case int8, int16, int32, int, int64, uint8, uint16, uint32, uint, uint64, float64, float32, json.Number:
		var num int64
		if num, err = ParseIntegerNumber(v, true); err != nil {
			return nil, err
		}
		return []uint64{uint64(num)}, nil
	default:
		rt := reflect.TypeOf(val)
		if rt.Kind() != reflect.Slice {
			break
		}
		rv := reflect.ValueOf(val)
		for i := 0; i < rv.Len(); i++ {
			var num int64
			if num, err = ParseIntegerNumber(rv.Index(i).Interface(), true); err != nil {
				return nil, err
			}
			res = append(res, uint64(num))
		}
	}
	return nil, fmt.Errorf("not suppoted type:%+v", v)
}

func (p *NumberRangeParser) ParseValue(v interface{}) (res []uint64, err error) {
	switch value := v.(type) {
	case string:
		opt := NewRangeDesc(value)
		if opt == nil {
			return nil, ErrBadRangeValue
		}
		for s := opt.start; s <= opt.end; s += opt.step {
			res = append(res, uint64(s))
		}
		return res, nil
	case []string:
		for _, s := range value {
			opt := NewRangeDesc(s)
			if opt == nil {
				return nil, ErrBadRangeValue
			}
			for s := opt.start; s <= opt.end; s += opt.step {
				res = append(res, uint64(s))
			}
		}
	case []interface{}:
		for _, si := range value {
			sv, ok := si.(string)
			if !ok {
				return nil, ErrBadRangeValue
			}
			var desc *RangeDesc
			if desc = NewRangeDesc(sv); desc == nil {
				return nil, ErrBadRangeValue
			}

			for s := desc.start; s <= desc.end; s += desc.step {
				res = append(res, uint64(s))
			}
		}
	default:
		return nil, ErrBadRangeValue
	}
	return res, nil
}
