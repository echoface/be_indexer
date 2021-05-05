package be_indexer

import (
	"github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestNewConjID(t *testing.T) {
	convey.Convey("test conjuntion id", t, func() {
		cases := [][3]int{
			{0, 0, 0},
			{1, 2, 3},
			{2, 0, 1},
			{12, 1, 20},
		}
		for _, cs := range cases {
			id := NewConjID(DocID(cs[0]), cs[1], cs[2])
			convey.So(id.DocID(), convey.ShouldEqual, cs[0])
			convey.So(id.Index(), convey.ShouldEqual, cs[1])
			convey.So(id.Size(), convey.ShouldEqual, cs[2])
		}
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
		convey.So(conj.size, convey.ShouldEqual, 1)

		conj.AddBoolExpr(NewBoolExpr("ip", true, NewValues2("localhost", "127.0.0.1")))
		convey.So(len(conj.Expressions), convey.ShouldEqual, 3)

		convey.So(conj.CalcConjSize(), convey.ShouldEqual, 2)
		convey.So(conj.size, convey.ShouldEqual, 2)

		convey.So(func() {
			conj.addExpression("age", true, NewValues2(1))
		}, convey.ShouldPanic)

	})
}
