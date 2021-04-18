package be_indexer

import (
	"fmt"
	"github.com/HuanGong/be_indexer/parser"
)

type (
	BEField string

	Values []interface{}

	// BoolValues expression a bool logic like: (in) [15,16,17], (not in) [shanghai,yz]
	BoolValues struct {
		Incl  bool   `json:"inc"`   // include: true exclude: false
		Value Values `json:"value"` // values can be parser parse to id
	}

	// BoolExprs expression a bool logic like: age (in) [15,16,17], city (not in) [shanghai,yz]
	BoolExprs struct {
		BoolValues
		Field BEField `json:"field"`
	}

	Assignments map[BEField]Values
)

func (ass Assignments) Size() (size int) {
	for _, v := range ass {
		if len(v) > 0 {
			size++
		}
	}
	return size
}

func NewBoolExpr(field BEField, inc bool, v Values) *BoolExprs {
	expr := &BoolExprs{
		Field: field,
		BoolValues: BoolValues{
			Value: v,
			Incl:  inc,
		},
	}
	return expr
}

//panic if invalid value
func NewValues(o ...interface{}) (res []interface{}) {
	for _, value := range o {
		if parser.IsValidValueType(value) {
			res = append(res, value)
		} else {
			panic(fmt.Errorf("not supported value types"))
		}
	}
	return
}

func NewValues2(v interface{}, o ...interface{}) (res []interface{}) {
	if !parser.IsValidValueType(v) {
		panic(fmt.Errorf("not supported value types"))
	}

	res = append(res, v)
	for _, value := range o {
		if parser.IsValidValueType(value) {
			res = append(res, value)
		} else {
			panic(fmt.Errorf("not supported value types"))
		}
	}
	return
}

func NewIntValues(o ...int) (res []interface{}) {
	res = make([]interface{}, len(o))
	for idx, optV := range o {
		res[idx] = optV
	}
	return
}

func NewIntValues2(v int, o ...int) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewInt32Values(o ...int32) (res []interface{}) {
	res = make([]interface{}, len(o))
	for idx, optV := range o {
		res[idx] = optV
	}
	return
}

func NewInt32Values2(v int32, o ...int32) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewInt64Values(o ...int64) (res []interface{}) {
	res = make([]interface{}, len(o))
	for idx, optV := range o {
		res[idx] = optV
	}
	return
}

func NewInt64Values2(v int64, o ...int64) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewStrValues(ss ...string) (res []interface{}) {
	res = make([]interface{}, len(ss))
	for idx, optV := range ss {
		res[idx] = optV
	}
	return
}

func NewStrValues2(v string, ss ...string) (res []interface{}) {
	res = make([]interface{}, len(ss)+1)
	res[0] = v
	for idx, optV := range ss {
		res[idx+1] = optV
	}
	return
}
