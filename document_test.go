package be_indexer

import (
	"github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestDocument_AddConjunction(t *testing.T) {
	convey.Convey("test doc option", t, func() {
		doc := NewDocument(12)
		doc.AddConjunction(NewConjunction().In("age", NewInt32Values2(12, 15)))

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
		doc.AddConjunction(NewConjunction().In("age", NewInt32Values2(12, 15)))

		convey.So(doc.ID, convey.ShouldEqual, 12)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		convey.So(doc.Cons[0].CalcConjSize(), convey.ShouldEqual, 1)
	})
}

func TestConjunction_AddBoolExpr(t *testing.T) {
	convey.Convey("test expressions", t, func() {
		conj := NewConjunction().NotIn("age", NewValues2(12, 14))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 1)
		convey.So(conj.Expressions["age"].Incl, convey.ShouldBeFalse)
		convey.So(len(conj.Expressions["age"].Value), convey.ShouldEqual, 2)

		conj.In("tag", NewValues2("tag1"))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 2)
		convey.So(conj.Expressions["tag"].Incl, convey.ShouldBeTrue)
		convey.So(len(conj.Expressions["tag"].Value), convey.ShouldEqual, 1)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj.AddBoolExpr(NewBoolExpr("ip", true, NewValues2("localhost", "127.0.0.1")))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 3)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)

		convey.So(func() {
			conj.addExpression("age", true, NewValues2(1))
		}, convey.ShouldPanic)

	})
}
