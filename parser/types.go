package parser

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/echoface/be_indexer/util"
)

type (
	// ValueTokenizer 将值转换为字符串列表，供索引构建 term
	// 这是 DefaultEntriesHolder 使用的 tokenizer 接口
	// 支持双向解析：索引阶段和查询阶段的解析逻辑可能不同
	ValueTokenizer interface {
		// TokenizeValue 索引阶段：解析布尔表达式的值（如 "30:90:1000" 展开为多个 geohash）
		TokenizeValue(v interface{}) ([]string, error)

		// TokenizeAssign 查询阶段：解析查询参数（如 [30.5, 98.2] 转为单个 geohash）
		TokenizeAssign(v interface{}) ([]string, error)
	}

	// ValueIDGenerator turn value into a unique id
	ValueIDGenerator interface {
		Name() string
		// ParseAssign parse query assign value into id-encoded ids
		ParseAssign(v interface{}) ([]uint64, error)

		// ParseValue parse bool expression value into id-encoded ids
		ParseValue(v interface{}) ([]uint64, error)
	}

	// FieldValueParser is an alias for ValueIDGenerator for backward compatibility
	// Deprecated: Use ValueIDGenerator instead
	FieldValueParser = ValueIDGenerator
)

func ParseIntegerNumber(v interface{}, floatToInt bool) (n int64, err error) {
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
		if floatToInt {
			if fv, err := strconv.ParseFloat(vf.String(), 64); err == nil {
				return int64(fv), nil
			}
		}
		return 0, fmt.Errorf("invalid number:%s", vf.String())
	case reflect.Float64, reflect.Float32:
		if floatToInt {
			return int64(vf.Float()), nil
		}
	default:
	}
	return 0, fmt.Errorf("not supported number type:%+v", v)
}

func ParseIntegers(v interface{}, floatToInt bool) (res []int64, err error) {
	if util.NilInterface(v) {
		return res, nil
	}
	switch t := v.(type) {
	case string, json.Number, int, int8, int16, int32,
		int64, uint, uint8, uint16, uint32, uint64, float64, float32:
		if num, err := ParseIntegerNumber(t, floatToInt); err == nil {
			return append(res, num), nil
		}
	case []int8, []int16, []int32, []int, []int64, []uint8, []uint16,
		[]uint32, []uint, []uint64, []float32, []float64, []json.Number, []string:
		rv := reflect.ValueOf(t)

		res = make([]int64, 0, rv.Len())
		var num int64
		for i := 0; i < rv.Len(); i++ {
			vi := rv.Index(i).Interface()
			if num, err = ParseIntegerNumber(vi, floatToInt); err != nil {
				return nil, err
			}
			res = append(res, num)
		}
		return res, nil
	case []interface{}:
		res = make([]int64, 0, len(t))
		for _, iv := range t {
			var num int64
			if num, err = ParseIntegerNumber(iv, floatToInt); err != nil {
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
