package parser

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/echoface/be_indexer/util"
)

type (
	CommonStrParser struct {
		EnableFloat2Int bool
		StrIDAllocator  IDAllocator
	}
)

func NewCommonParser() *CommonStrParser {
	parser := &CommonStrParser{
		EnableFloat2Int: true,
		StrIDAllocator:  NewHashAllocator(nil),
	}
	return parser
}

func (p *CommonStrParser) Name() string {
	return "common"
}

func (p *CommonStrParser) ParseAssign(v interface{}) (values []uint64, e error) {
	if util.NilInterface(v) {
		return values, nil
	}

	switch t := v.(type) {
	case string:
		if id, found := p.StrIDAllocator.FindStringID(t); found {
			return []uint64{id}, nil
		}
		return nil, nil
	case json.Number:
		if id, found := p.StrIDAllocator.FindStringID(string(t)); found {
			return []uint64{id}, nil
		}
		return nil, nil
	case int, uint, uint8, int8, int16, uint16, int32, uint32, int64, uint64:
		s := fmt.Sprintf("%v", t)
		if v, found := p.StrIDAllocator.FindStringID(s); found {
			return []uint64{v}, nil
		}
		return nil, nil
	case float64, float32:
		if !p.EnableFloat2Int {
			break
		}
		vf := reflect.ValueOf(v)
		s := fmt.Sprintf("%v", uint64(vf.Float()))
		if num, found := p.StrIDAllocator.FindStringID(s); found {
			return []uint64{num}, nil
		}
		return
	case []float64, []float32:
		if !p.EnableFloat2Int {
			break
		}
		rv := reflect.ValueOf(t)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", uint64(rv.Index(i).Float()))
			if num, found := p.StrIDAllocator.FindStringID(s); found {
				res = append(res, num)
			}
		}
		return res, nil
	case []int8, []int16, []int32, []int, []int64,
		[]uint8, []uint16, []uint32, []uint, []uint64, []json.Number, []string:
		rv := reflect.ValueOf(t)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			if id, ok := p.StrIDAllocator.FindStringID(s); ok {
				res = append(res, id)
			}
		}
		return res, nil
	case []interface{}:
		res := make([]uint64, 0, len(t))
		for _, iv := range t {
			if id, ok := p.findInterfaceID(iv); ok {
				res = append(res, id)
			}
		}
		return res, nil
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}

func (p *CommonStrParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case int, uint, uint8, int8, int16, uint16, int32, uint32, int64, uint64, string, json.Number:
		s := fmt.Sprintf("%v", value)
		return []uint64{p.StrIDAllocator.AllocStringID(s)}, nil
	case float64, float32:
		if !p.EnableFloat2Int {
			return nil, fmt.Errorf("type float not supported")
		}
		vf := reflect.ValueOf(v)
		s := fmt.Sprintf("%v", uint64(vf.Float()))
		return []uint64{p.StrIDAllocator.AllocStringID(s)}, nil
	case []float64, []float32:
		if !p.EnableFloat2Int {
			return nil, fmt.Errorf("type []float not supported")
		}
		rv := reflect.ValueOf(value)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			res = append(res, p.StrIDAllocator.AllocStringID(s))
		}
		return res, nil
	case []int8, []int16, []int32, []int, []int64, []string,
		[]uint8, []uint16, []uint32, []uint, []uint64, []json.Number:
		rv := reflect.ValueOf(value)
		res := make([]uint64, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			s := fmt.Sprintf("%v", rv.Index(i))
			res = append(res, p.StrIDAllocator.AllocStringID(s))
		}
		return res, nil
	case []interface{}:
		res := make([]uint64, 0, len(value))
		for _, iv := range value {
			if num, err := p.allocInterfaceID(iv); err != nil {
				return nil, err
			} else {
				res = append(res, num)
			}
		}
		return res, nil
	default:
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("type [%s] not supported", valueType.String())
}

// interface value need `single` value
func (p *CommonStrParser) allocInterfaceID(iv interface{}) (uint64, error) {
	switch value := iv.(type) {
	case string:
		return p.StrIDAllocator.AllocStringID(value), nil
	case json.Number:
		return p.StrIDAllocator.AllocStringID(string(value)), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		str := fmt.Sprintf("%v", value)
		return p.StrIDAllocator.AllocStringID(str), nil
	case float64, float32:
		if !p.EnableFloat2Int {
			return 0, fmt.Errorf("type float not supported")
		}
		vf := reflect.ValueOf(iv)
		s := fmt.Sprintf("%v", uint64(vf.Float()))
		return p.StrIDAllocator.AllocStringID(s), nil
	default:
		return 0, fmt.Errorf("need string type")
	}
}

// interface value need `single` value
func (p *CommonStrParser) findInterfaceID(iv interface{}) (uint64, bool) {
	switch value := iv.(type) {
	case string:
		return p.StrIDAllocator.FindStringID(value)
	case json.Number:
		return p.StrIDAllocator.FindStringID(string(value))
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		str := fmt.Sprintf("%v", value)
		return p.StrIDAllocator.FindStringID(str)
	case float64, float32:
		if !p.EnableFloat2Int {
			return 0, false
		}
		vf := reflect.ValueOf(iv)
		s := fmt.Sprintf("%v", uint64(vf.Float()))
		return p.StrIDAllocator.FindStringID(s)
	default:
		rt := reflect.TypeOf(iv)
		fmt.Println("not support type:", rt.String(), ", value:", iv)
	}
	return 0, false
}
