package beindexer

import (
	"github.com/smartystreets/goconvey/convey"
	"testing"
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

		doc.Prepare()
		convey.So(doc.Cons[0].id, convey.ShouldEqual, NewConjID(12, 0, 1))
	})
}
