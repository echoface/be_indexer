package roaringidx

import (
	"encoding/json"
	"fmt"

	"github.com/echoface/be_indexer"
)

const (
	MaxConjunctions = 0xFF

	MaxDocumentBits = 56
	MaxDocumentID   = 0xFFFFFFFFFFFFFF
)

type (
	// ConjunctionID [8bit idx + 56 bit document id]
	ConjunctionID uint64

	// Conjunction a standalone DNF boolean logic unit. its contains a serial data with logic `and` between them
	// a hooked processor provider a change for users to intercept a Handler for None-Boolean-Logic when retrieve
	Conjunction struct {
		id          ConjunctionID
		Expressions map[be_indexer.BEField]*be_indexer.BoolExprs
	}

	IndexDocument struct {
		ID           int64          `json:"id"` //  Need Less than 56bit see ConjunctionID for more detail
		Conjunctions []*Conjunction `json:"conjunctions,omitempty"`
	}
)

func NewDocument(id int64) *IndexDocument {
	doc := &IndexDocument{
		ID: id,
	}
	if !doc.Valid() {
		panic("document id overflow, limit:max 56bit value")
	}
	return doc
}

func (doc *IndexDocument) AddConjunction(conj *Conjunction) {
	conj.id = NewConjunctionID(len(doc.Conjunctions), doc.ID)
	doc.Conjunctions = append(doc.Conjunctions, conj)
}

func (doc *IndexDocument) AddConjunctions(conj *Conjunction, others ...*Conjunction) {
	conj.id = NewConjunctionID(len(doc.Conjunctions), doc.ID)
	doc.Conjunctions = append(doc.Conjunctions, conj)
	for _, conj := range others {
		conj.id = NewConjunctionID(len(doc.Conjunctions), doc.ID)
		doc.Conjunctions = append(doc.Conjunctions, conj)
	}
}

// ReIndexConjunction re-calc conj id, common use case: after unmarshal from json
func (doc *IndexDocument) ReIndexConjunction() {
	for idx, conj := range doc.Conjunctions {
		conj.id = NewConjunctionID(idx, doc.ID)
	}
}

func (doc *IndexDocument) String() string {
	c, _ := json.Marshal(doc)
	return string(c)
}

func (doc *IndexDocument) Valid() bool {
	if doc.ID > MaxDocumentID {
		return false
	}
	for idx, conj := range doc.Conjunctions {
		if int(conj.id.Idx()) != idx || conj.id.DocID() != doc.ID {
			return false
		}
	}
	return true
}

func NewConjunctionID(idx int, doc int64) ConjunctionID {
	if idx > MaxConjunctions {
		panic("conjunction idx overflow")
	}
	return ConjunctionID(uint64(idx)<<MaxDocumentBits | uint64(doc&MaxDocumentID))
}

// DocID [8bit idx + 56bit document]
func (id ConjunctionID) DocID() int64 {
	return int64(uint64(id) & MaxDocumentID)
}

func (id ConjunctionID) Idx() uint8 {
	return uint8(uint64(id) >> MaxDocumentBits & MaxConjunctions)
}

func NewConjunction() *Conjunction {
	return &Conjunction{
		id:          0,
		Expressions: map[be_indexer.BEField]*be_indexer.BoolExprs{},
	}
}

func (conj *Conjunction) AddExpression3(field string, include bool, values be_indexer.Values) *Conjunction {
	expr := be_indexer.NewBoolExpr(be_indexer.BEField(field), include, values)
	return conj.AddExpression(expr)
}

func (conj *Conjunction) AddExpression(expr *be_indexer.BoolExprs) *Conjunction {
	if _, hit := conj.Expressions[expr.Field]; hit {
		panic(fmt.Sprintf("field replicated in one conjunction, id:%d field:%s", conj.id, expr.Field))
	}
	conj.Expressions[expr.Field] = expr
	return conj
}

func (conj *Conjunction) DocID() int64 {
	return conj.id.DocID()
}

func (conj *Conjunction) Include(field be_indexer.BEField, values be_indexer.Values) *Conjunction {
	return conj.AddExpression(be_indexer.NewBoolExpr(field, true, values))
}

func (conj *Conjunction) Exclude(field be_indexer.BEField, values be_indexer.Values) *Conjunction {
	return conj.AddExpression(be_indexer.NewBoolExpr(field, false, values))
}
