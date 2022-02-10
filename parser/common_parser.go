package parser

import (
	"fmt"
	"reflect"
)

type (
	CommonStrParser struct {
		idAlloc IDAllocator
	}
)

func NewCommonStrParser() FieldValueParser {
	return &CommonStrParser{
		idAlloc: NewIDAllocatorImpl(),
	}
}

func NewCommonParserWithAllocator(alloc IDAllocator) FieldValueParser {
	return &CommonStrParser{
		idAlloc: alloc,
	}
}

func (p *CommonStrParser) ParseAssign(v interface{}) (values []uint64, e error) {
	switch t := v.(type) {
	case string:
		v, found := p.idAlloc.FindStringID(&t)
		if !found {
			return
		}
		return []uint64{v}, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64, float64, float32:
		s := fmt.Sprintf("%v", t)
		v, found := p.idAlloc.FindStringID(&s)
		if !found {
			return
		}
		return []uint64{v}, nil
	default:
		valueType := reflect.TypeOf(v)
		return nil, fmt.Errorf("value type [%s] not support", valueType.String())
	}
}

func (p *CommonStrParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case string:
		return []uint64{p.idAlloc.AllocStringID(value)}, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64, float32, float64:
		s := fmt.Sprintf("%v", value)
		return []uint64{p.idAlloc.AllocStringID(s)}, nil
	default:
		valueType := reflect.TypeOf(v)
		return nil, fmt.Errorf("value type [%s] not support", valueType.String())
	}
}
