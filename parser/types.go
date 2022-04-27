package parser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
)

type (
	// FieldValueParser turn value into a unique id
	FieldValueParser interface {
		// ParseAssign parse query assign value into id-encoded ids
		ParseAssign(v interface{}) ([]uint64, error)

		// ParseValue parse bool expression value into id-encoded ids
		ParseValue(v interface{}) ([]uint64, error)
	}
)

func ParseIntegerNumber(v interface{}, f2i bool) (n int64, err error) {
	vf := reflect.ValueOf(v)
	switch tv := v.(type) {
	case int, int8, int16, int32, int64:
		return vf.Int(), nil
	case uint, uint8, uint16, uint32, uint64:
		return int64(vf.Uint()), nil
	case string:
		return strconv.ParseInt(tv, 10, 64)
	case float64, float32:
		if f2i {
			return int64(vf.Float()), nil
		}
	case json.Number:
		if vi, e := tv.Int64(); e == nil {
			return vi, nil
		}
		if vfloat, e := tv.Float64(); e == nil && f2i {
			return int64(vfloat), nil
		}
	default:
	}
	return 0, fmt.Errorf("not supprted number type:%+v", v)
}
