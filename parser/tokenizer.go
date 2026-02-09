package parser

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// funcTokenizer 是一个适配器，将函数转换为 ValueTokenizer 接口
// 双向解析使用相同逻辑
type funcTokenizer func(v interface{}) ([]string, error)

func (f funcTokenizer) TokenizeValue(v interface{}) ([]string, error) {
	return f(v)
}

func (f funcTokenizer) TokenizeAssign(v interface{}) ([]string, error) {
	return f(v)
}

// defaultTokenizer 是默认的 ValueTokenizer 实现
// 双向解析使用相同逻辑：简单地将值转为字符串
type defaultTokenizer struct{}

func (d *defaultTokenizer) TokenizeValue(v interface{}) ([]string, error) {
	return ValuesToStrings(v)
}

func (d *defaultTokenizer) TokenizeAssign(v interface{}) ([]string, error) {
	return ValuesToStrings(v)
}

// NewDefaultTokenizer 创建默认的 ValueTokenizer
func NewDefaultTokenizer() ValueTokenizer {
	return &defaultTokenizer{}
}

// ValueToString 将单个值转换为字符串
func ValueToString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case json.Number:
		return string(val), nil
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%v", val), nil
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%v", val), nil
	case float64, float32:
		return fmt.Sprintf("%v", val), nil
	default:
		return "", fmt.Errorf("unsupported value type: %T", v)
	}
}

// ValuesToStrings 将值（可以是单个值或切片）转换为字符串列表
func ValuesToStrings(v interface{}) ([]string, error) {
	if v == nil {
		return nil, nil
	}

	switch val := v.(type) {
	// Single values
	case string, json.Number, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float64, float32:
		s, err := ValueToString(val)
		if err != nil {
			return nil, err
		}
		return []string{s}, nil

	// Slice values
	case []string:
		return val, nil
	case []json.Number:
		result := make([]string, len(val))
		for i, v := range val {
			result[i] = string(v)
		}
		return result, nil
	case []int, []int8, []int16, []int32, []int64:
		rv := reflect.ValueOf(val)
		result := make([]string, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = fmt.Sprintf("%v", rv.Index(i).Interface())
		}
		return result, nil
	case []uint, []uint8, []uint16, []uint32, []uint64:
		rv := reflect.ValueOf(val)
		result := make([]string, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = fmt.Sprintf("%v", rv.Index(i).Interface())
		}
		return result, nil
	case []float64, []float32:
		rv := reflect.ValueOf(val)
		result := make([]string, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = fmt.Sprintf("%v", rv.Index(i).Interface())
		}
		return result, nil
	case []interface{}:
		result := make([]string, len(val))
		for i, elem := range val {
			s, err := ValueToString(elem)
			if err != nil {
				return nil, err
			}
			result[i] = s
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported value type: %T", v)
	}
}
