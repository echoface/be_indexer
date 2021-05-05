package be_indexer

import "errors"

type (
	//ConjID max support 56bit len
	ConjID uint64

	Conjunction struct {
		id          ConjID
		size        int                     // 如果通过序列还/反序列化方式构造， 需要手动调用CalcConjSize
		Expressions map[BEField]*BoolValues `json:"exprs"` // 同一个Conj内不允许重复的Field
	}
)

func NewConjID(docID DocID, index, size int) ConjID {
	u := (uint64(docID) << 16) | (uint64(index) << 8) | uint64(size)
	return ConjID(u)
}

func (id ConjID) Size() int {
	return int(id & 0xFF)
}

func (id ConjID) Index() int {
	return int((id >> 8) & 0xFF)
}

func (id ConjID) DocID() DocID {
	return DocID((id >> 16) & 0xFFFFFFFF)
}

func NewConjunction() *Conjunction {
	return &Conjunction{
		Expressions: make(map[BEField]*BoolValues),
	}
}

// any value in values is a **true** expression
func (conj *Conjunction) In(field BEField, values Values) *Conjunction {
	conj.addExpression(field, true, values)
	return conj
}

// any value in values is a **false** expression
func (conj *Conjunction) NotIn(field BEField, values Values) *Conjunction {
	conj.addExpression(field, false, values)
	return conj
}

func (conj *Conjunction) AddBoolExpr(expr *BoolExprs) *Conjunction {
	conj.addExpression(expr.Field, expr.Incl, expr.Value)
	return conj
}

/*
append boolean expression,
don't allow same field added twice in one conjunction
*/
func (conj *Conjunction) AddBoolExprs(exprs ...*BoolExprs) {
	for _, expr := range exprs {
		conj.AddBoolExpr(expr)
	}
}

func (conj *Conjunction) addExpression(field BEField, inc bool, values Values) {
	if _, ok := conj.Expressions[field]; ok {
		panic(errors.New("conj don't allow one field show up twice"))
	}
	conj.Expressions[field] = &BoolValues{
		Incl:  inc,
		Value: values,
	}
	if len(conj.Expressions) > 0xFF {
		panic(errors.New("too much indexing field, maximum 256 field supported"))
	}
}

func (conj *Conjunction) CalcConjSize() (size int) {
	for _, bv := range conj.Expressions {
		if bv.Incl {
			size++
		}
	}
	conj.size = size
	return
}
