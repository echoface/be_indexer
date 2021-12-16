package be_indexer

import (
	"errors"
)

type (
	DocID     int64
	DocIDList []DocID

	Conjunction struct { // 每个conjunction 内的field 逻辑为且， 参考DNF定义
		Expressions map[BEField]*BoolValues `json:"exprs"` // 同一个Conj内不允许重复的Field
	}

	Document struct {
		ID   DocID          `json:"id"`   // 只支持int32最大值个Doc
		Cons []*Conjunction `json:"cons"` // conjunction之间的关系是或，具体描述可以看论文的表述
	}
)

func NewDocument(id DocID) *Document {
	return &Document{
		ID:   id,
		Cons: make([]*Conjunction, 0),
	}
}

func (s DocIDList) Contain(id DocID) bool {
	for _, v := range s {
		if v == id {
			return true
		}
	}
	return false
}

func (s DocIDList) Sub(other DocIDList) (r DocIDList) {
BASE:
	for _, v := range s {
		for _, c := range other {
			if v == c {
				continue BASE
			}
		}
		r = append(r, v)
	}
	return
}

//Len sort API
func (s DocIDList) Len() int           { return len(s) }
func (s DocIDList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s DocIDList) Less(i, j int) bool { return s[i] < s[j] }

/*AddConjunction 一组完整的expression， 必须是完整一个描述文档的DNF Bool表达的条件组合*/
func (doc *Document) AddConjunction(cons ...*Conjunction) {
	for _, conj := range cons {
		doc.Cons = append(doc.Cons, conj)
	}
}

func (doc *Document) AddConjunctions(conj *Conjunction, others ...*Conjunction) {
	doc.Cons = append(doc.Cons, conj)
	for _, conj := range others {
		doc.Cons = append(doc.Cons, conj)
	}
}

func NewConjunction() *Conjunction {
	return &Conjunction{
		Expressions: make(map[BEField]*BoolValues),
	}
}

// In any value in values is a **true** expression
func (conj *Conjunction) In(field BEField, values Values) *Conjunction {
	conj.addExpression(field, true, values)
	return conj
}

// NotIn any value in values is a **false** expression
func (conj *Conjunction) NotIn(field BEField, values Values) *Conjunction {
	conj.addExpression(field, false, values)
	return conj
}

func (conj *Conjunction) AddBoolExpr(expr *BooleanExpr) *Conjunction {
	conj.addExpression(expr.Field, expr.Incl, expr.Value)
	return conj
}

// AddBoolExprs append boolean expression,
// don't allow same field added twice in one conjunction
func (conj *Conjunction) AddBoolExprs(exprs ...*BooleanExpr) {
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
}

func (conj *Conjunction) CalcConjSize() (size int) {
	for _, bv := range conj.Expressions {
		if bv.Incl {
			size++
		}
	}
	return
}

func (conj *Conjunction) AddExpression(expr *BooleanExpr) *Conjunction {
	conj.addExpression(expr.Field, expr.Incl, expr.Value)
	return conj
}

func (conj *Conjunction) Include(field BEField, values Values) *Conjunction {
	return conj.AddExpression(NewBoolExpr(field, true, values))
}

func (conj *Conjunction) Exclude(field BEField, values Values) *Conjunction {
	return conj.AddExpression(NewBoolExpr(field, false, values))
}

func (conj *Conjunction) AddExpression3(field string, include bool, values Values) *Conjunction {
	expr := NewBoolExpr(BEField(field), include, values)
	return conj.AddExpression(expr)
}
