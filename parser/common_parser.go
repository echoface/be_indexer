package parser

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/echoface/be_indexer/util"
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
	if util.NilInterface(v) {
		return values, nil
	}

	switch t := v.(type) {
	case string:
		v, found := p.idAlloc.FindStringID(&t)
		if !found {
			return
		}
		return []uint64{v}, nil
	case json.Number:
		str := string(t)
		v, found := p.idAlloc.FindStringID(&str)
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
	case []float64, []float32:
		if !p.float2Int {
			break
		}
		rv := reflect.ValueOf(t)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", uint64(rv.Index(i).Float()))
			if num, found := p.idAlloc.FindStringID(&s); found {
				res = append(res, num)
			}
		}
		return res, nil
	case []int8, []int16, []int32, []int, []int64, []uint8, []uint16,
		[]uint32, []uint, []uint64, []json.Number, []string:
		rv := reflect.ValueOf(t)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			if num, found := p.idAlloc.FindStringID(&s); found {
				res = append(res, num)
			}
		}
		return res, nil
	case []interface{}:
		res := make([]uint64, 0, len(t))
		for _, iv := range t {
			s := fmt.Sprintf("%v", iv)
			res = append(res, p.idAlloc.AllocStringID(s))
		}
		return res, nil
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}

func (p *CommonStrParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case int, uint, uint8, int8, int32, uint32, int64, uint64, string, json.Number:
		s := fmt.Sprintf("%v", value)
		return []uint64{p.idAlloc.AllocStringID(s)}, nil
	case float64, float32:
		if p.float2Int {
			return nil, fmt.Errorf("type float not supported")
		}
		vf := reflect.ValueOf(v)
		s := fmt.Sprintf("%v", uint64(vf.Float()))
		return []uint64{p.idAlloc.AllocStringID(s)}, nil
	case []float64, []float32:
		if !p.float2Int {
			return nil, fmt.Errorf("type []float not supported")
		}
		rv := reflect.ValueOf(value)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			res = append(res, p.idAlloc.AllocStringID(s))
		}
		return res, nil
	case []int8, []int16, []int32, []int, []int64, []string,
		[]uint8, []uint16, []uint32, []uint, []uint64, []json.Number:
		rv := reflect.ValueOf(value)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			res = append(res, p.idAlloc.AllocStringID(s))
		}
		return res, nil
	case []interface{}:
		res := make([]uint64, 0, len(value))
		for _, iv := range value {
			s := fmt.Sprintf("%v", iv)
			res = append(res, p.idAlloc.AllocStringID(s))
		}
		return res, nil
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("type [%s] not supported", valueType.String())
}
