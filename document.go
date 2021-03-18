package beindexer

import (
	"fmt"
)

type (
	// 如果序列化/反序列化生成需要手动调用
	Document struct {
		ID   int32          `json:"id"`   //只支持int32最大值个Doc
		Cons []*Conjunction `json:"cons"` //conjunction之间的关系是或，具体描述可以看论文的表述
	}
)

func NewDocument(id int32) *Document {
	return &Document{
		ID:   id,
		Cons: make([]*Conjunction, 0),
	}
}

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
	for idx, conj := range doc.Cons {
		size := conj.CalcConjSize()
		conj.id = NewConjID(doc.ID, idx, size)
	}
}
