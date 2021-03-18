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

func NewCommonStrParser(allocator IDAllocator) FieldValueParser {
	return &CommonStrParser{
		idAlloc: allocator,
	}
}

func (p *CommonStrParser) ValueIDs(v interface{}) (values []uint64, e error) {
	switch t := v.(type) {
	case string:
		fmt.Printf("get id for:%s\n", t)
		v, found := p.idAlloc.FindStringID(&t)
		if !found {
			return
		}
		return []uint64{v}, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64, float64, float32:
		s := fmt.Sprintf("%v", t)
		fmt.Printf("get id for:%s\n", s)
		v, found := p.idAlloc.FindStringID(&s)
		if !found {
			return
		}
		return []uint64{v}, nil
	case []string:
		for _, s := range t {
			fmt.Printf("get id for:%s\n", s)
			v, found := p.idAlloc.FindStringID(&s)
			if found {
				values = append(values, v)
			}
		}
		return
	case []int, []uint, []uint8, []int8, []int32, []uint32, []int64, []uint64, []interface{}:
		fmt.Println("type: []x", t)
		s := reflect.ValueOf(t)
		for i := 0; i < s.Len(); i++ {
			strNumber := fmt.Sprintf("%v", s.Index(i).Interface())
			fmt.Printf("get id for:%s\n", strNumber)
			if v, found := p.idAlloc.FindStringID(&strNumber); found {
				values = append(values, v)
			}
		}
		return
	default:
		valueType := reflect.TypeOf(v)
		return nil, fmt.Errorf("value type [%s] not support", valueType.String())
	}
}

func (p *CommonStrParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case string:
		fmt.Printf("gen id for:%s\n", value)
		return []uint64{p.idAlloc.AllocStringID(value)}, nil
	case []string:
		result := make([]uint64, len(value))
		for idx, s := range value {
			fmt.Printf("gen id for:%s\n", s)
			result[idx] = p.idAlloc.AllocStringID(s)
		}
		return result, nil
	case int, uint, uint8, int8, int32, uint32, int64, uint64, float32, float64:
		s := fmt.Sprintf("%v", value)
		fmt.Printf("gen id for:%s\n", s)
		return []uint64{p.idAlloc.AllocStringID(s)}, nil
	case []int, []uint, []uint8, []int8, []int32, []uint32, []int64, []uint64, []interface{}:
		s := reflect.ValueOf(value)
		result := make([]uint64, s.Len())
		for i := 0; i < s.Len(); i++ {
			strNumber := fmt.Sprintf("%v", s.Index(i).Interface())
			fmt.Printf("gen id for:%s\n", strNumber)
			result[i] = p.idAlloc.AllocStringID(strNumber)
		}
		return result, nil
	default:
		valueType := reflect.TypeOf(v)
		return nil, fmt.Errorf("value type [%s] not support", valueType.String())
	}
}
