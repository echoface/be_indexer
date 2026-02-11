package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestDocument_AddConjunction(t *testing.T) {
	convey.Convey("test doc option", t, func() {
		doc := core.NewDocument(12)
		doc.AddConjunction(core.NewConjunction().In("age", core.NewInt32Values(12, 15)))

		convey.So(doc.ID, convey.ShouldEqual, 12)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		convey.ShouldPanic(func() {
			doc.AddConjunction(core.NewConjunction())
		}, convey.ShouldPanic)
	})

}

func TestDocument_Prepare(t *testing.T) {

	convey.Convey("test doc prepare", t, func() {
		doc := core.NewDocument(12)
		doc.AddConjunction(core.NewConjunction().In("age", core.NewInt32Values(12, 15)))

		convey.So(doc.ID, convey.ShouldEqual, 12)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		convey.So(doc.Cons[0].CalcConjSize(), convey.ShouldEqual, 1)
	})
}

func TestConjunction_AddBoolExpr(t *testing.T) {
	convey.Convey("test expressions", t, func() {
		conj := core.NewConjunction().NotIn("age", core.NewIntValues(12, 14))
		convey.So(len(conj.Predicates), convey.ShouldEqual, 1)
		convey.So(conj.Predicates["age"][0].Incl, convey.ShouldBeFalse)

		conj.In("tag", core.NewStrValues("tag1"))
		convey.So(len(conj.Predicates), convey.ShouldEqual, 2)
		convey.So(conj.Predicates["tag"][0].Incl, convey.ShouldBeTrue)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj.AddPredicates(core.NewPredicate("ip", true, core.NewStrValues("localhost", "127.0.0.1")))
		convey.So(len(conj.Predicates), convey.ShouldEqual, 3)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)

		convey.So(func() {
			conj.In("age", 1)
		}, convey.ShouldNotPanic)

	})
}

func TestDocument_String(t *testing.T) {
	convey.Convey("test string", t, func() {
		doc := core.NewDocument(100)
		doc.AddConjunction(
			core.NewConjunction().LessThan("kkk", 15),
			core.NewConjunction().GreaterThan("age", 15),
			core.NewConjunction().Between("kkk", 15, 20),
			core.NewConjunction().In("age", core.NewIntValues(1, 2, 3)).NotIn("age", 5),
			core.NewConjunction().NotIn("tag", core.NewStrValues("a", "b")).Include("age", core.NewIntValues(18)),
		)
		t.Log(doc.String())
	})
}

func TestDocument_AddConjunctions(t *testing.T) {
	convey.Convey("test conj size", t, func() {
		conj := core.NewConjunction().
			In("age", []int{20, 30, 40}).NotIn("age", []int{30, 50}).
			NotIn("city", core.NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj = core.NewConjunction().
			In("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			NotIn("city", core.NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 1)

		conj = core.NewConjunction().
			In("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			In("city", core.NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)

		conj = core.NewConjunction().
			NotIn("age", []int{20, 30, 40}).In("age", []int{30, 50}).
			In("city", core.NewStrValues("bj", "sh"))
		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)
	})

}
