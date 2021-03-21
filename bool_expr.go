package beindexer

import (
	"be_indexer/parser"
	"fmt"
)

type (
	BEField string

	Values []interface{}

	BoolValues struct {
		Incl  bool   `json:"inc"`   // include: true exclude: false
		Value Values `json:"value"` // values can be parser parse to id
	}

	// expression: age (in) [15,16,17], city (not in) [shanghai,yz]
	BoolExprs struct {
		BoolValues
		Field BEField `json:"field"`
	}

	Assignments map[BEField]Values
)

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
func NewValues(v interface{}, o ...interface{}) (res []interface{}) {
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

func NewIntValues(v int, o ...int) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewInt32Values(v int32, o ...int) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewInt64Values(v int64, o ...int) (res []interface{}) {
	res = make([]interface{}, len(o)+1)
	res[0] = v
	for idx, optV := range o {
		res[idx+1] = optV
	}
	return
}

func NewStrValues(v string, ss ...string) (res []interface{}) {
	res = make([]interface{}, len(ss)+1)
	res[0] = v
	for idx, optV := range ss {
		res[idx+1] = optV
	}
	return
}
