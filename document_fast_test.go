package be_indexer

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestNewConjunctionWithCapacity(t *testing.T) {
	convey.Convey("test NewConjunctionWithCapacity", t, func() {
		conj := NewConjunctionWithCapacity(5)
		convey.So(conj, convey.ShouldNotBeNil)
		convey.So(conj.Expressions, convey.ShouldNotBeNil)
	})
}

func TestConjPool(t *testing.T) {
	convey.Convey("test Conjunction pool", t, func() {
		// 从池中获取
		c1 := GetConjunction()
		convey.So(c1, convey.ShouldNotBeNil)

		// 添加一些内容
		c1.InInt("age", 18, 25)
		convey.So(len(c1.Expressions), convey.ShouldEqual, 1)

		// 归还到池中
		PutConjunction(c1)

		// 再次获取，检查是否重置
		c2 := GetConjunction()
		convey.So(len(c2.Expressions), convey.ShouldEqual, 0)
		PutConjunction(c2)
	})
}

func TestDocPool(t *testing.T) {
	convey.Convey("test Document pool", t, func() {
		// 从池中获取
		d1 := GetDocument()
		convey.So(d1, convey.ShouldNotBeNil)

		// 设置 ID
		d1.ID = 100

		// 添加 Conjunction
		conj := GetConjunction()
		conj.InInt("age", 18)
		d1.AddConjunction(conj)

		// 归还到池中
		PutDocument(d1)

		// 再次获取
		d2 := GetDocument()
		convey.So(d2.ID, convey.ShouldEqual, 0) // 应该被重置
		convey.So(len(d2.Cons), convey.ShouldEqual, 0)
		PutDocument(d2)
	})
}

func TestFastConjBuilder(t *testing.T) {
	convey.Convey("test FastConjBuilder", t, func() {
		conj := ConjBuilder().
			InInt("age", 18, 25, 30).
			NotInInt("city", 1, 2).
			GT("score", 100).
			LT("height", 200).
			Between("weight", 50, 100).
			Build()

		convey.So(conj, convey.ShouldNotBeNil)
		convey.So(len(conj.Expressions), convey.ShouldEqual, 5)
		convey.So(conj.Expressions["age"], convey.ShouldNotBeNil)
		convey.So(conj.Expressions["score"], convey.ShouldNotBeNil)
	})
}

func TestFastConjBuilder_BuildTo(t *testing.T) {
	convey.Convey("test FastConjBuilder BuildTo", t, func() {
		cb := ConjBuilder()
		cb.InInt("age", 18, 25)

		// 写入预分配的 Conjunction
		conj := GetConjunction()
		cb.BuildTo(conj)

		convey.So(len(conj.Expressions), convey.ShouldEqual, 1)
		PutConjunction(conj)
	})
}

func TestFastDocBuilder(t *testing.T) {
	convey.Convey("test FastDocBuilder", t, func() {
		// 创建第一个 Conjunction
		cb1 := ConjBuilder()
		cb1.InInt("age", 18, 25)
		cb1.InStr("city", "beijing")

		// 创建第二个 Conjunction
		cb2 := ConjBuilder()
		cb2.InInt("vip", 1)

		doc := DocBuilder().
			SetID(100).
			AddConj(cb1).
			AddConj(cb2).
			Build()

		convey.So(doc.ID, convey.ShouldEqual, 100)
		convey.So(len(doc.Cons), convey.ShouldEqual, 2)
	})
}

func TestFastNewConj(t *testing.T) {
	convey.Convey("test NewConj", t, func() {
		conj := NewConj()
		convey.So(conj, convey.ShouldNotBeNil)
		PutConjunction(conj)
	})
}

func TestFastNewDoc(t *testing.T) {
	convey.Convey("test NewDoc", t, func() {
		doc := NewDoc(200)
		convey.So(doc.ID, convey.ShouldEqual, 200)
		PutDocument(doc)
	})
}

func TestInInt(t *testing.T) {
	convey.Convey("test InInt", t, func() {
		conj := GetConjunction()
		conj.InInt("age", 18)
		conj.InInt("age", 25, 30) // 同一个字段多次调用

		convey.So(len(conj.Expressions), convey.ShouldEqual, 1)
		ageExprs := conj.Expressions["age"]
		convey.So(len(ageExprs), convey.ShouldEqual, 2)

		PutConjunction(conj)
	})
}

func BenchmarkFastConjBuilder(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conj := ConjBuilder().
			InInt("age", 18, 25, 30).
			InStr("city", "beijing", "shanghai").
			NotInInt("status", 0).
			GT("score", 100).
			LT("height", 200).
			Between("weight", 50, 100).
			Build()
		_ = conj
		PutConjunction(conj)
	}
}

func BenchmarkFastConjBuilder_BuildTo(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cb := ConjBuilder()
		cb.InInt("age", 18, 25, 30)
		cb.InStr("city", "beijing", "shanghai")
		cb.NotInInt("status", 0)
		cb.GT("score", 100)
		cb.LT("height", 200)
		cb.Between("weight", 50, 100)

		conj := GetConjunction()
		cb.BuildTo(conj)
		_ = conj
		PutConjunction(conj)
	}
}

func BenchmarkTraditionalConj(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conj := NewConjunction()
		conj.In("age", []int64{18, 25, 30})
		conj.In("city", []string{"beijing", "shanghai"})
		conj.NotIn("status", []int{0})
		conj.GreaterThan("score", 100)
		conj.LessThan("height", 200)
		conj.Between("weight", 50, 100)
		_ = conj
	}
}

func BenchmarkWithPool(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conj := GetConjunction()
		conj.In("age", []int64{18, 25, 30})
		conj.In("city", []string{"beijing", "shanghai"})
		conj.NotIn("status", []int{0})
		conj.GreaterThan("score", 100)
		conj.LessThan("height", 200)
		conj.Between("weight", 50, 100)
		_ = conj
		PutConjunction(conj)
	}
}

func BenchmarkInInt(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conj := GetConjunction()
		conj.InInt("age", 18, 25, 30)
		conj.InStr("city", "beijing", "shanghai")
		conj.NotInInt("status", 0)
		conj.GreaterThan("score", 100)
		conj.LessThan("height", 200)
		conj.Between("weight", 50, 100)
		_ = conj
		PutConjunction(conj)
	}
}
