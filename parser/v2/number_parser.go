package v2

import (
	"fmt"
	"reflect"
	"strconv"
)

type (
	NumberParser struct {
	}
)

func NewNumberParser() FieldValueParser {
	return &NumberParser{}
}

func (p *NumberParser) ParseAssign(v interface{}) (values []uint64, e error) {
	return p.ParseValue(v)
}

func (p *NumberParser) ParseValue(v interface{}) ([]uint64, error) {
	switch t := v.(type) {
	case string:
		number, e := strconv.ParseInt(t, 10, 64)
		if e != nil {
			return nil, e
		}
		return []uint64{uint64(number)}, nil
	case int, int8, int16, int32, int64:
		number := reflect.ValueOf(t).Int()
		return []uint64{uint64(number)}, nil
	case uint, uint8, uint16, uint32, uint64:
		number := reflect.ValueOf(t).Uint()
		return []uint64{number}, nil
	default:
		valueType := reflect.TypeOf(v)
		return nil, fmt.Errorf("value type [%s] not support", valueType.String())
	}
}
