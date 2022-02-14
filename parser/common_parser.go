package parser

import (
	"fmt"
	"reflect"
)

type (
	CommonParserOpt struct {
		f2i     bool
		idAlloc IDAllocator
	}
	CommonStrParser struct {
		float2Int bool
		idAlloc   IDAllocator
	}
)

func NewCommonStrParser() *CommonStrParser {
	return &CommonStrParser{
		float2Int: true,
		idAlloc:   NewIDAllocatorImpl(),
	}
}

func NewCommonParserWithOption(opt CommonParserOpt) *CommonStrParser {
	parser := &CommonStrParser{
		float2Int: opt.f2i,
		idAlloc:   opt.idAlloc,
	}
	if opt.idAlloc == nil {
		opt.idAlloc = NewIDAllocatorImpl()
	}
	return parser
}

func NewCommonParserWithAllocator(alloc IDAllocator) *CommonStrParser {
	return &CommonStrParser{
		float2Int: true,
		idAlloc:   alloc,
	}
}

func (p *CommonStrParser) IDGen() IDAllocator {
	return p.idAlloc
}

func (p *CommonStrParser) ParseAssign(v interface{}) (values []uint64, e error) {
	switch t := v.(type) {
	case string:
		v, found := p.idAlloc.FindStringID(&t)
		if !found {
			return
		}
		return []uint64{v}, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64:
		s := fmt.Sprintf("%v", t)
		v, found := p.idAlloc.FindStringID(&s)
		if !found {
			return
		}
		return []uint64{v}, nil
	case float64, float32:
		if p.float2Int {
			vf := reflect.ValueOf(v)
			s := fmt.Sprintf("%v", uint64(vf.Float()))
			num, found := p.idAlloc.FindStringID(&s)
			if !found {
				return
			}
			return []uint64{num}, nil
		}
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}

func (p *CommonStrParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case string:
		return []uint64{p.idAlloc.AllocStringID(value)}, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64:
		s := fmt.Sprintf("%v", value)
		return []uint64{p.idAlloc.AllocStringID(s)}, nil
	case float64, float32:
		if p.float2Int {
			vf := reflect.ValueOf(v)
			s := fmt.Sprintf("%v", uint64(vf.Float()))
			return []uint64{p.idAlloc.AllocStringID(s)}, nil
		}
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}
