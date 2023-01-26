package parser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/echoface/be_indexer/util"
)

type (
	// FieldValueParser turn value into a unique id
	FieldValueParser interface {
		Name() string

		// ParseAssign parse query assign value into id-encoded ids
		ParseAssign(v interface{}) ([]uint64, error)

		// ParseValue parse bool expression value into id-encoded ids
		ParseValue(v interface{}) ([]uint64, error)
	}
)

func ParseIntegerNumber(v interface{}, f2i bool) (n int64, err error) {
	vf := reflect.ValueOf(v)
	switch vf.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return vf.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(vf.Uint()), nil
	case reflect.String:
		if num, err := strconv.ParseInt(vf.String(), 10, 64); err == nil {
			return num, nil
		}
		if f2i {
			if fv, err := strconv.ParseFloat(vf.String(), 64); err == nil {
				return int64(fv), nil
			}
		}
		return 0, fmt.Errorf("invalid number:%s", vf.String())
	case reflect.Float64, reflect.Float32:
		if f2i {
			return int64(vf.Float()), nil
		}
	default:
	}
	return 0, fmt.Errorf("not supprted number type:%+v", v)
}

func ParseIntergers(v interface{}, f2i bool) (res []int64, err error) {
	if util.NilInterface(v) {
		return res, nil
	}
	switch t := v.(type) {
	case string, json.Number, int, int8, int16, int32,
		int64, uint, uint8, uint16, uint32, uint64, float64, float32:
		if num, err := ParseIntegerNumber(t, f2i); err == nil {
			return append(res, num), nil
		}
	case []int8, []int16, []int32, []int, []int64, []uint8, []uint16,
		[]uint32, []uint, []uint64, []float32, []float64, []json.Number, []string:
		rv := reflect.ValueOf(t)

		res = make([]int64, 0, rv.Len())
		var num int64
		for i := 0; i < rv.Len(); i++ {
			vi := rv.Index(i).Interface()
			if num, err = ParseIntegerNumber(vi, f2i); err != nil {
				return nil, err
			}
			res = append(res, num)
		}
		return res, nil
	case []interface{}:
		res = make([]int64, 0, len(t))
		for _, iv := range t {
			var num int64
			if num, err = ParseIntegerNumber(iv, f2i); err != nil {
				return nil, fmt.Errorf("value:%v not a number value", iv)
			}
			res = append(res, num)
		}
		return res, nil
	default:
		break
	}
	valueType := reflect.TypeOf(v)
	return nil, fmt.Errorf("value type [%s] not support", valueType.String())
}
