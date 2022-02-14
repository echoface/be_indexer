package parser

import (
	"fmt"
	"strconv"
	"strings"
)

var ErrBadRangeValue = fmt.Errorf("NumberRangeParser need:'start:end:step'")

/*
NumberRangeParser parse syntax like format: start:end:step
step ist optional, it will generate start, start+step, start+2*stem ....
*/
type (
	// NumberRangeParser format: start:end:step; the `step` is optional
	NumberRangeParser struct {
	}

	RangeDesc struct {
		start int64
		end   int64
		step  int64
	}
)

func NewNumRangeParser() FieldValueParser {
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

// ParseAssign only single number supported, float will round into integer
func (p *NumberRangeParser) ParseAssign(v interface{}) (res []uint64, err error) {
	num, err := ParseIntegerNumber(v, true)
	if err != nil {
		return nil, err
	}
	return []uint64{uint64(num)}, nil
}

func (p *NumberRangeParser) ParseValue(v interface{}) (res []uint64, err error) {
	content, ok := v.(string)
	if !ok {
		return nil, ErrBadRangeValue
	}
	opt := NewRangeDesc(content)
	if opt == nil {
		return nil, ErrBadRangeValue
	}
	for s := opt.start; s <= opt.end; s += opt.step {
		res = append(res, uint64(s))
	}
	return res, nil
}
