package be_indexer

import (
	"fmt"
)

type (
	DocID     uint32
	DocIDList []DocID

	Document struct {
		ID   DocID          `json:"id"`   //只支持int32最大值个Doc
		Cons []*Conjunction `json:"cons"` //conjunction之间的关系是或，具体描述可以看论文的表述
	}
)

func NewDocument(id DocID) *Document {
	return &Document{
		ID:   id,
		Cons: make([]*Conjunction, 0),
	}
}

func (l DocIDList) Contain(id DocID) bool {
	for _, v := range l {
		if v == id {
			return true
		}
	}
	return false
}

//Entries sort API
func (s DocIDList) Len() int           { return len(s) }
func (s DocIDList) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s DocIDList) Less(i, j int) bool { return s[i] < s[j] }

/*一组完整的expression， 必须是完整一个描述文档的DNF Bool表达的条件组合*/
func (doc *Document) AddConjunction(cons ...*Conjunction) {
	for _, conj := range cons {
		if len(conj.Expressions) == 0 {
			panic(fmt.Errorf("invalid conjunction"))
		}
		doc.Cons = append(doc.Cons, conj)
	}
}

//计算生成doc内部的私有数据
func (doc *Document) Prepare() {
	if len(doc.Cons) >= 0xFF {
		panic(fmt.Errorf("max 256 conjuctions per document limitation"))
	}
	for idx, conj := range doc.Cons {
		size := conj.CalcConjSize()
		conj.id = NewConjID(doc.ID, idx, size)
	}
}
