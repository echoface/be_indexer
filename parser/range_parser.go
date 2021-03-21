package parser

import (
	"fmt"
	"strconv"
	"strings"
)

/*
NumberRangeParser parse syntax like format: start:end:step
step ist optional, it will generate start, start+step, start+2*stem ....
*/
type (
	// format: start:end:step step ist optional
	NumberRangeParser struct {
		idAlloc IDAllocator
	}

	numRangeOption struct {
		start int64
		end   int64
		step  int64
	}
)

func NewNumRangeParser(allocator IDAllocator) FieldValueParser {
	return &NumberRangeParser{
		idAlloc: allocator,
	}
}

// format: start:end:step
func (p *NumberRangeParser) parseOption(v string) *numRangeOption {
	opt := &numRangeOption{
		step: 1,
	}
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
	if len(vs) > 2 {
		if opt.step, err = strconv.ParseInt(vs[2], 10, 64); err != nil {
			return nil
		}
	}
	return opt
}

// get api
func (p *NumberRangeParser) ParseAssign(v interface{}) (res []uint64, err error) {
	num, err := parseNumber(v)
	if err != nil {
		return nil, err
	}
	if id, ok := p.idAlloc.FindNumID(num); ok {
		return append(res, id), nil
	}
	return nil, nil
}

// parse api
func (p *NumberRangeParser) ParseValue(v interface{}) (res []uint64, err error) {
	content, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("not a format string value")
	}
	opt := p.parseOption(content)
	if opt == nil {
		return nil, fmt.Errorf("invalid format like start:end:step")
	}
	for s := opt.start; s <= opt.end; s += opt.step {
		res = append(res, p.idAlloc.AllocNumID(s))
	}
	return res, nil
}
