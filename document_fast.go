package be_indexer

import (
	"sync"
)

// ============================================================================
// 高性能 Conjunction 构建器 - 减少 GC 和方法调用开销
// ============================================================================

// InInt 添加 int64 类型的 IN 条件
func (conj *Conjunction) InInt(field string, v ...int64) *Conjunction {
	if len(v) == 1 {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  true,
			Value: v[0],
		})
	} else {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  true,
			Value: v,
		})
	}
	return conj
}

// InIntSlice 添加 []int64 的 IN 条件
func (conj *Conjunction) InIntSlice(field string, v []int64) *Conjunction {
	conj.addExpression(BEField(field), BoolValues{
		Incl:  true,
		Value: v,
	})
	return conj
}

// InStr 添加字符串的 IN 条件
func (conj *Conjunction) InStr(field string, v ...string) *Conjunction {
	if len(v) == 1 {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  true,
			Value: v[0],
		})
	} else {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  true,
			Value: v,
		})
	}
	return conj
}

// NotInInt 添加 int64 类型的 NOT IN 条件
func (conj *Conjunction) NotInInt(field string, v ...int64) *Conjunction {
	if len(v) == 1 {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  false,
			Value: v[0],
		})
	} else {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  false,
			Value: v,
		})
	}
	return conj
}

// NotInStr 添加字符串的 NOT IN 条件
func (conj *Conjunction) NotInStr(field string, v ...string) *Conjunction {
	if len(v) == 1 {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  false,
			Value: v[0],
		})
	} else {
		conj.addExpression(BEField(field), BoolValues{
			Incl:  false,
			Value: v,
		})
	}
	return conj
}

// ============================================================================
// 对象池 - 复用 Document/Conjunction 对象
// ============================================================================

var (
	conjPool = sync.Pool{
		New: func() interface{} {
			return &Conjunction{
				Expressions: make(map[BEField][]*BoolValues, 4),
			}
		},
	}

	docPool = sync.Pool{
		New: func() interface{} {
			return &Document{
				Cons: make([]*Conjunction, 0, 4),
			}
		},
	}
)

// GetConjunction 从池中获取一个 Conjunction
func GetConjunction() *Conjunction {
	c := conjPool.Get().(*Conjunction)
	// 重置状态
	for k := range c.Expressions {
		delete(c.Expressions, k)
	}
	return c
}

// PutConjunction 将 Conjunction 归还到池中
func PutConjunction(c *Conjunction) {
	if c != nil {
		conjPool.Put(c)
	}
}

// GetDocument 从池中获取一个 Document
func GetDocument() *Document {
	d := docPool.Get().(*Document)
	// 重置状态
	d.ID = 0
	for i := range d.Cons {
		d.Cons[i] = nil
	}
	d.Cons = d.Cons[:0]
	return d
}

// PutDocument 将 Document 归还到池中
func PutDocument(d *Document) {
	if d != nil {
		// 归还所有 Conjunction
		for _, c := range d.Cons {
			PutConjunction(c)
		}
		docPool.Put(d)
	}
}

// ============================================================================
// 高性能构建器 - 使用栈上预分配减少 GC
// ============================================================================

// ConjBuilder 使用栈上预分配的快速构建器
// 特点：Build 后不需要释放，直接使用返回的 Conjunction
func ConjBuilder() *conjBuilder {
	return &conjBuilder{}
}

type conjBuilder struct {
	exprs []namedExpr
}

type namedExpr struct {
	field BEField
	bv    BoolValues
}

// InInt 添加 int64 IN 条件
func (b *conjBuilder) InInt(field string, v ...int64) *conjBuilder {
	if len(v) == 1 {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  true,
				Value: v[0],
			},
		})
	} else {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  true,
				Value: v,
			},
		})
	}
	return b
}

// NotInInt 添加 int64 NOT IN 条件
func (b *conjBuilder) NotInInt(field string, v ...int64) *conjBuilder {
	if len(v) == 1 {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  false,
				Value: v[0],
			},
		})
	} else {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  false,
				Value: v,
			},
		})
	}
	return b
}

// InStr 添加字符串 IN 条件
func (b *conjBuilder) InStr(field string, v ...string) *conjBuilder {
	if len(v) == 1 {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  true,
				Value: v[0],
			},
		})
	} else {
		b.exprs = append(b.exprs, namedExpr{
			field: BEField(field),
			bv: BoolValues{
				Incl:  true,
				Value: v,
			},
		})
	}
	return b
}

// GT 添加大于条件
func (b *conjBuilder) GT(field string, v int64) *conjBuilder {
	b.exprs = append(b.exprs, namedExpr{
		field: BEField(field),
		bv: BoolValues{
			Operator: ValueOptGT,
			Incl:     true,
			Value:    v,
		},
	})
	return b
}

// LT 添加小于条件
func (b *conjBuilder) LT(field string, v int64) *conjBuilder {
	b.exprs = append(b.exprs, namedExpr{
		field: BEField(field),
		bv: BoolValues{
			Operator: ValueOptLT,
			Incl:     true,
			Value:    v,
		},
	})
	return b
}

// Between 添加区间条件
func (b *conjBuilder) Between(field string, lo, hi int64) *conjBuilder {
	b.exprs = append(b.exprs, namedExpr{
		field: BEField(field),
		bv: BoolValues{
			Operator: ValueOptBetween,
			Incl:     true,
			Value:    []int64{lo, hi},
		},
	})
	return b
}

// Build 完成构建，返回 Conjunction（从池中获取）
func (b *conjBuilder) Build() *Conjunction {
	conj := GetConjunction()
	for _, e := range b.exprs {
		conj.Expressions[e.field] = append(conj.Expressions[e.field], &e.bv)
	}
	return conj
}

// BuildTo 将结果写入预分配的 Conjunction（零分配）
func (b *conjBuilder) BuildTo(conj *Conjunction) {
	for _, e := range b.exprs {
		conj.Expressions[e.field] = append(conj.Expressions[e.field], &e.bv)
	}
}

// Reset 重置构建器
func (b *conjBuilder) Reset() {
	b.exprs = b.exprs[:0]
}

// ============================================================================
// DocBuilder 使用栈上预分配的 Document 快速构建器
// ============================================================================

// DocBuilder 创建一个 Document 构建器
func DocBuilder() *docBuilder {
	return &docBuilder{}
}

type docBuilder struct {
	id    DocID
	conjs []*Conjunction
}

// SetID 设置文档 ID
func (b *docBuilder) SetID(id DocID) *docBuilder {
	b.id = id
	return b
}

// AddConj 添加一个 Conjunction
func (b *docBuilder) AddConj(cb *conjBuilder) *docBuilder {
	b.conjs = append(b.conjs, cb.Build())
	return b
}

// AddConjTo 添加 Conjunction 到预分配的 Document
func (b *docBuilder) AddConjTo(cb *conjBuilder, conj *Conjunction) {
	cb.BuildTo(conj)
	b.conjs = append(b.conjs, conj)
}

// Build 完成构建，返回 Document
func (b *docBuilder) Build() *Document {
	doc := GetDocument()
	doc.ID = b.id
	doc.Cons = append(doc.Cons, b.conjs...)
	return doc
}

// BuildTo 将结果写入预分配的 Document
func (b *docBuilder) BuildTo(doc *Document) {
	doc.ID = b.id
	doc.Cons = append(doc.Cons, b.conjs...)
}

// Reset 重置构建器
func (b *docBuilder) Reset() {
	b.id = 0
	b.conjs = b.conjs[:0]
}

// ============================================================================
// 预分配工具函数
// ============================================================================

// NewConjunctionWithCapacity 创建一个预分配容量的 Conjunction
func NewConjunctionWithCapacity(fieldCount int) *Conjunction {
	if fieldCount <= 0 {
		fieldCount = 4
	}
	return &Conjunction{
		Expressions: make(map[BEField][]*BoolValues, fieldCount),
	}
}

// NewDocumentWithCapacity 创建一个预分配容量的 Document
func NewDocumentWithCapacity(id DocID, conjCount int) *Document {
	if conjCount <= 0 {
		conjCount = 1
	}
	return &Document{
		ID:   id,
		Cons: make([]*Conjunction, 0, conjCount),
	}
}

// ============================================================================
// 便捷构建函数
// ============================================================================

// NewConj 创建一个新的 Conjunction（从池中获取）
func NewConj() *Conjunction {
	return GetConjunction()
}

// NewDoc 创建一个新的 Document（从池中获取）
func NewDoc(id DocID) *Document {
	doc := GetDocument()
	doc.ID = id
	return doc
}
