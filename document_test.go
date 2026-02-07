package be_indexer

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestDocument_AddConjunction(t *testing.T) {
	convey.Convey("test doc option", t, func() {
		doc := NewDocument(12)
		doc.AddConjunction(NewConjunction().In("age", NewInt32Values(12, 15)))

		convey.So(doc.ID, convey.ShouldEqual, 12)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		convey.ShouldPanic(func() {
			doc.AddConjunction(NewConjunction())
		}, convey.ShouldPanic)
	})

}

func TestDocument_Prepare(t *testing.T) {

	convey.Convey("test doc prepare", t, func() {
		doc := NewDocument(12)
		doc.AddConjunction(NewConjunction().In("age", NewInt32Values(12, 15)))

		convey.So(doc.ID, convey.ShouldEqual, 12)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		convey.So(doc.Cons[0].CalcConjSize(), convey.ShouldEqual, 1)
	})
}

func TestConjunction_AddBoolExpr(t *testing.T) {
	convey.Convey("test expressions", t, func() {
		conj := NewConjunction().NotIn("age", NewIntValues(12, 14))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 1)
		convey.So(conj.Expressions["age"][0].Incl, convey.ShouldBeFalse)

		conj.In("tag", NewStrValues("tag1"))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 2)
		convey.So(conj.Expressions["tag"][0].Incl, convey.ShouldBeTrue)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj.AddBoolExprs(NewBoolExpr("ip", true, NewStrValues("localhost", "127.0.0.1")))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 3)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)

		convey.So(func() {
			conj.In("age", 1)
		}, convey.ShouldNotPanic)

	})
}

func TestDocument_String(t *testing.T) {
	convey.Convey("test string", t, func() {
		doc := NewDocument(100)
		doc.AddConjunction(
			NewConjunction().LessThan("kkk", 15),
			NewConjunction().GreaterThan("age", 15),
			NewConjunction().Between("kkk", 15, 20),
			NewConjunction().In("age", NewIntValues(1, 2, 3)).NotIn("age", 5),
			NewConjunction().NotIn("tag", NewStrValues("a", "b")).Include("age", NewIntValues(18)),
		)
		t.Log(doc.String())
	})
}

func TestDocument_AddConjunctions(t *testing.T) {
	convey.Convey("test conj size", t, func() {
		conj := NewConjunction().
			In("age", []int{20, 30, 40}).NotIn("age", []int{30, 50}).
			NotIn("city", NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj = NewConjunction().
			In("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			NotIn("city", NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj = NewConjunction().
			In("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			In("city", NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)

		conj = NewConjunction().
			NotIn("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			In("city", NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)
	})

}
