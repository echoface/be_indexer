package util

import (
	"reflect"
)

func NilInterface(v interface{}) bool {
	if v == nil {
		return true
	}
	switch reflect.TypeOf(v).Kind() {
	case reflect.Ptr, reflect.Map, reflect.Array, reflect.Chan, reflect.Slice:
		return reflect.ValueOf(v).IsNil()
	}
	return false
}

type Integer interface {
	int8 | int16 | int32 | int | int64 | uint8 | uint16 | uint32 | uint | uint64
}

func CastIntegers[F Integer, T Integer](from []F) []T {
	res := make([]T, len(from))
	for i, e := range from {
		res[i] = T(e)
	}
	return res
}

func CastInteger[F Integer, T Integer](from F) T {
	return T(from)
}
