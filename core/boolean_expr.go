package core

import (
	"encoding/json"
	"fmt"

	"github.com/echoface/be_indexer/util"
)

type (
	BEField = string

	// ValueOpt value数值的描述符; 注意这里将其与最终的bool逻辑运算符区分开;
	// 描述一个值: >5 代表了所有数值空间中[5, *)所有的值; 结合布尔描述
	// 中的Incl/Excl 才构成一个布尔描述; 简而言之它用于描述存在"哪些值"
	ValueOpt int

	Values interface{}

	// ValueExpr expression a bool logic like: (in) [15,16,17], (not in) [shanghai,yz]
	// 默认opt: ValueOptEQ
	// 包含: [5, *)  的布尔描述等同于 "排除: (-*, 5)"
	ValueExpr struct {
		Incl     bool     `json:"inc"`                // include: true exclude: false
		Value    Values   `json:"value"`              // values can be parser parse to id
		Operator ValueOpt `json:"operator,omitempty"` // value对应数值空间的描述符, 默认: EQ
	}

	// Predicate expression a bool logic like: age (in) [15,16,17], city (not in) [shanghai,yz]
	Predicate struct {
		ValueExpr
		Field BEField `json:"field"`
	}

	Assignments map[BEField]Values
)

const (
	// ValueOptEQ ...数值范围描述符
	ValueOptEQ      ValueOpt = 0
	ValueOptGT      ValueOpt = 1
	ValueOptLT      ValueOpt = 2
	ValueOptBetween ValueOpt = 3
)

func (ass Assignments) Size() (size int) {
	for _, v := range ass {
		if util.NilInterface(v) {
			continue
		}
		size++
	}
	return size
}

func NewPredicate2(field BEField, expr ValueExpr) *Predicate {
	return &Predicate{expr, field}
}

func NewPredicate(field BEField, inc bool, v Values) *Predicate {
	expr := &Predicate{
		Field: field,
		ValueExpr: ValueExpr{
			Value: v,
			Incl:  inc,
		},
	}
	return expr
}

func NewIntValues(v int, o ...int) Values {
	return append([]int{v}, o...)
}

func NewInt32Values(v int32, o ...int32) Values {
	return append([]int32{v}, o...)
}

func NewInt64Values(v int64, o ...int64) Values {
	return append([]int64{v}, o...)
}

func NewStrValues(v string, ss ...string) Values {
	return append([]string{v}, ss...)
}

func NewGTValueExpr(value int64) ValueExpr {
	return NewValueExpr(ValueOptGT, value, true)
}

func NewLTValueExpr(value int64) ValueExpr {
	return NewValueExpr(ValueOptLT, value, true)
}

func NewValueExpr(op ValueOpt, value Values, incl bool) ValueExpr {
	return ValueExpr{
		Operator: op,
		Incl:     incl,
		Value:    value,
	}
}

func (v *ValueExpr) BooleanToken() string {
	if v.Incl {
		return "in"
	}
	return "not"
}

func (v *ValueExpr) String() string {
	return fmt.Sprintf("%s %s%v", v.BooleanToken(), v.OperatorName(), v.Value)
}

func (v *ValueExpr) OperatorName() string {
	switch v.Operator {
	case ValueOptGT:
		return ">"
	case ValueOptLT:
		return "<"
	case ValueOptBetween:
		return "between"
	default:
		break
	}
	return ""
}

func (v *ValueExpr) JSONString() string {
	data, _ := json.Marshal(v)
	return string(data)
}
