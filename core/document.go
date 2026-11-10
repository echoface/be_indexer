package core

import (
	"encoding/json"
	"fmt"
	"strings"
)

type (
	DocID     int64
	DocIDList []DocID

	// Conjunction is an AND of predicates, grouped by field name.
	Conjunction struct {
		Constraints map[FieldName][]*Constraint `json:"predicates"` // 同字段最多一个 Include，可有多个 Exclude
	}

	Document struct {
		ID      DocID          `json:"id"`      // 范围 [-MaxDocID, MaxDocID]
		Version uint64         `json:"version"` // 业务版本号；增量冲突由 Mutation.Version 处理
		Cons    []*Conjunction `json:"cons"`    // conjunction之间的关系是或，具体描述可以看论文的表述
	}
)

func NewDocument(id DocID) *Document {
	return &Document{
		ID:      id,
		Version: 0,
		Cons:    make([]*Conjunction, 0),
	}
}

// Contains reports whether id is present in the list.
func (s DocIDList) Contains(id DocID) bool {
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

// Len sort API
func (s DocIDList) Len() int           { return len(s) }
func (s DocIDList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s DocIDList) Less(i, j int) bool { return s[i] < s[j] }

// AddConjunction 一组完整的expression， 必须是完整一个描述文档的DNF Bool表达的条件组合*/
func (doc *Document) AddConjunction(cons ...*Conjunction) *Document {
	for _, conj := range cons {
		doc.Cons = append(doc.Cons, conj)
	}
	return doc
}

func (doc *Document) AddConjunctions(conj *Conjunction, others ...*Conjunction) *Document {
	doc.Cons = append(doc.Cons, conj)
	for _, conj := range others {
		doc.Cons = append(doc.Cons, conj)
	}
	return doc
}

func (doc *Document) JSONString() string {
	data, _ := json.Marshal(doc)
	return string(data)
}

// String a more compacted string
func (doc *Document) String() string {
	strBuilder := strings.Builder{}
	strBuilder.WriteString(fmt.Sprintf("doc:%d, cons:[\n", doc.ID))
	cnt := len(doc.Cons)
	for i, conj := range doc.Cons {
		strBuilder.WriteString(fmt.Sprintf("\t%d:%s", i, conj.String()))
		cnt--
		if cnt > 0 {
			strBuilder.WriteString(",\n")
		}
	}
	strBuilder.WriteString("\n]")
	return strBuilder.String()
}

func NewConjunction() *Conjunction {
	return &Conjunction{
		Constraints: make(map[FieldName][]*Constraint),
	}
}

// In any value in values is a **true** expression
// In is an alias for Include. Prefer Include for clarity.
func (conj *Conjunction) In(field FieldName, values Values) *Conjunction {
	return conj.Include(field, values)
}

// NotIn any value in values is a **false** expression
// NotIn is an alias for Exclude. Prefer Exclude for clarity.
func (conj *Conjunction) NotIn(field FieldName, values Values) *Conjunction {
	return conj.Exclude(field, values)
}

func (conj *Conjunction) Include(field FieldName, values Values) *Conjunction {
	conj.addPredicate(field, NewConstraint(ValueOpEQ, values, true))
	return conj
}

func (conj *Conjunction) Exclude(field FieldName, values Values) *Conjunction {
	conj.addPredicate(field, NewConstraint(ValueOpEQ, values, false))
	return conj
}

func (conj *Conjunction) GreaterThan(field FieldName, value int64) *Conjunction {
	conj.AddPredicates(&Predicate{
		Field:      field,
		Constraint: NewGTConstraint(value),
	})
	return conj
}

func (conj *Conjunction) LessThan(field FieldName, value int64) *Conjunction {
	conj.AddPredicates(&Predicate{
		Field:      field,
		Constraint: NewLTConstraint(value),
	})
	return conj
}

func (conj *Conjunction) Between(field FieldName, l, h int64) *Conjunction {
	conj.AddPredicates(&Predicate{
		Field:      field,
		Constraint: NewConstraint(ValueOpBetween, []int64{l, h}, true),
	})
	return conj
}

// AddPredicates appends field constraints. Build-time validation allows at most
// one Include per field and any number of Excludes; this method only appends.
func (conj *Conjunction) AddPredicates(exprs ...*Predicate) *Conjunction {
	for _, expr := range exprs {
		conj.addPredicate(expr.Field, expr.Constraint)
	}
	return conj
}

// AddPredicateValues is a convenience that creates an EQ predicate from raw values.
func (conj *Conjunction) AddPredicateValues(field string, include bool, values Values) *Conjunction {
	conj.addPredicate(FieldName(field), NewConstraint(ValueOpEQ, values, include))
	return conj
}

func (conj *Conjunction) addPredicate(field FieldName, boolValues Constraint) {
	conj.Constraints[field] = append(conj.Constraints[field], &boolValues)
}

func (conj *Conjunction) JSONString() string {
	data, _ := json.Marshal(conj)
	return string(data)
}

func (conj *Conjunction) String() string {
	strBuilder := strings.Builder{}
	strBuilder.WriteString("{")
	cnt := len(conj.Constraints)
	for field, exprs := range conj.Constraints {
		strBuilder.WriteString(fmt.Sprintf("%s (", field))
		for i, expr := range exprs {
			if i != 0 {
				strBuilder.WriteString(",")
			}
			strBuilder.WriteString(expr.String())
		}
		cnt--
		if cnt > 0 {
			strBuilder.WriteString(") and ")
		} else {
			strBuilder.WriteString(")")
		}
	}
	strBuilder.WriteString("}")
	return strBuilder.String()
}

// IncludeCount returns K: the number of fields with an Include constraint.
// Valid conjunctions have at most one Include per field, so this is also the
// number of Include constraints. Excludes and multiple values do not increase K.
func (conj *Conjunction) IncludeCount() (size int) {
	for _, bvs := range conj.Constraints {
	EXPR:
		for _, expr := range bvs {
			if expr.Incl {
				size++
				break EXPR
			}
		}
	}
	return size
}

// FieldCount returns the number of distinct field keys, including Exclude-only
// fields. It does not count individual constraints or their values.
func (conj *Conjunction) FieldCount() (size int) {
	return len(conj.Constraints)
}
