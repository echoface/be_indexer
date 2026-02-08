package be_indexer

import (
	"strconv"
	"strings"
)

// ============================================================================
// 值类型定义 - 强类型值，避免 interface{} 的类型安全问题
// ============================================================================

// IntValue int64 类型值
type IntValue int64

// Int32Value int32 类型值
type Int32Value int32

// Int64Value int64 类型值
type Int64Value int64

// StrValue string 类型值
type StrValue string

// Float64Value float64 类型值
type Float64Value float64

// BoolValue bool 类型值
type BoolValue bool

// ============================================================================
// 值工厂函数 - 类型安全地创建值
// ============================================================================

// Int 创建一个 int64 值（兼容各种 int 类型）
func Int[T int | int8 | int16 | int32 | int64 | uint | uint8 | uint16 | uint32](v T) IntValue {
	return IntValue(v)
}

// Int64 创建一个 int64 值
func Int64(v int64) IntValue {
	return IntValue(v)
}

// Int32 创建一个 int32 值
func Int32(v int32) Int32Value {
	return Int32Value(v)
}

// Str 创建一个 string 值
func Str(v string) StrValue {
	return StrValue(v)
}

// Float64 创建一个 float64 值
func Float64(v float64) Float64Value {
	return Float64Value(v)
}

// Bool 创建一个 bool 值
func Bool(v bool) BoolValue {
	return BoolValue(v)
}

// ============================================================================
// 类型断言辅助函数 - 确保值的类型安全
// ============================================================================

// AsInt 将值转换为 []int64（如果可能）
func AsInt(v Values) ([]int64, bool) {
	switch val := v.(type) {
	case []int64:
		return val, true
	case []int:
		result := make([]int64, len(val))
		for i, v := range val {
			result[i] = int64(v)
		}
		return result, true
	case []int32:
		result := make([]int64, len(val))
		for i, v := range val {
			result[i] = int64(v)
		}
		return result, true
	case []uint:
		result := make([]int64, len(val))
		for i, v := range val {
			result[i] = int64(v)
		}
		return result, true
	case []IntValue:
		result := make([]int64, len(val))
		for i, v := range val {
			result[i] = int64(v)
		}
		return result, true
	case []Int64Value:
		result := make([]int64, len(val))
		for i, v := range val {
			result[i] = int64(v)
		}
		return result, true
	default:
		// 尝试解析字符串
		if s, ok := val.(string); ok {
			return ParseInts(s)
		}
		return nil, false
	}
}

// AsStr 将值转换为 []string（如果可能）
func AsStr(v Values) ([]string, bool) {
	switch val := v.(type) {
	case []string:
		return val, true
	case []StrValue:
		result := make([]string, len(val))
		for i, v := range val {
			result[i] = string(v)
		}
		return result, true
	default:
		return nil, false
	}
}

// ParseInts 解析逗号分隔的整数字符串
func ParseInts(s string) ([]int64, bool) {
	parts := strings.Split(s, ",")
	result := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, false
		}
		result = append(result, v)
	}
	return result, len(result) > 0
}

// ParseFloats 解析逗号分隔的浮点数字符串
func ParseFloats(s string) ([]float64, bool) {
	parts := strings.Split(s, ",")
	result := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, false
		}
		result = append(result, v)
	}
	return result, len(result) > 0
}
