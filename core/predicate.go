package core

import (
	"encoding/json"
	"fmt"

	"github.com/echoface/be_indexer/util"
)

type (
	// FieldName identifies a field in a rule, schema, or query assignment.
	FieldName = string

	// ValueOp specifies the value comparison within a Constraint.
	// Incl selects inclusion or exclusion of the resulting value set.
	ValueOp int

	Values interface{}

	// Constraint describes a value set and whether matching it includes or
	// excludes a candidate. It has no field name; Predicate binds it to a field.
	// The zero Operator is EQ. Supported operators depend on the field's encoder.
	Constraint struct {
		Incl     bool    `json:"inc"`                // include: true exclude: false
		Value    Values  `json:"value"`              // scalar or collection accepted by the encoder
		Operator ValueOp `json:"operator,omitempty"` // comparison operator; defaults to EQ
	}

	// Predicate is one logical condition: a field and its constraint,
	// such as age > 18 or city not in ["shanghai", "beijing"].
	Predicate struct {
		Constraint
		Field FieldName `json:"field"`
	}

	// Assignments supplies query-time values by field, e.g. {"age": 20}.
	Assignments map[FieldName]Values
)

const (
	// ValueOpEQ matches any supplied value (set membership for a collection).
	ValueOpEQ ValueOp = 0
	// ValueOpGT and ValueOpLT are strict integer comparisons in the range encoder.
	ValueOpGT ValueOp = 1
	ValueOpLT ValueOp = 2
	// ValueOpBetween describes a closed integer interval [lo, hi].
	ValueOpBetween ValueOp = 3
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

// NewPredicateWithConstraint creates a Predicate from a pre-built Constraint.
// Prefer this when the expression is already constructed (e.g. range predicates).
func NewPredicateWithConstraint(field FieldName, expr Constraint) *Predicate {
	return &Predicate{expr, field}
}

func NewPredicate(field FieldName, inc bool, v Values) *Predicate {
	expr := &Predicate{
		Field: field,
		Constraint: Constraint{
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

func NewGTConstraint(value int64) Constraint {
	return NewConstraint(ValueOpGT, value, true)
}

func NewLTConstraint(value int64) Constraint {
	return NewConstraint(ValueOpLT, value, true)
}

func NewConstraint(op ValueOp, value Values, incl bool) Constraint {
	return Constraint{
		Operator: op,
		Incl:     incl,
		Value:    value,
	}
}

func (v *Constraint) BooleanToken() string {
	if v.Incl {
		return "in"
	}
	return "not"
}

func (v *Constraint) String() string {
	return fmt.Sprintf("%s %s%v", v.BooleanToken(), v.OperatorName(), v.Value)
}

func (v *Constraint) OperatorName() string {
	switch v.Operator {
	case ValueOpGT:
		return ">"
	case ValueOpLT:
		return "<"
	case ValueOpBetween:
		return "between"
	default:
		break
	}
	return ""
}

func (v *Constraint) JSONString() string {
	data, _ := json.Marshal(v)
	return string(data)
}
