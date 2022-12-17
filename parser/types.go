package parser

import (
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
