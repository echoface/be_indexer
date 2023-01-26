package roaringidx

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestNewConjunctionID(t *testing.T) {
	Convey("test conj id", t, func() {

		v, err := NewConjunctionID(0, 0)
		So(err, ShouldBeNil)
		So(v.Idx(), ShouldEqual, 0)
		So(v.DocID(), ShouldEqual, 0)

		v, err = NewConjunctionID(-1, 0)
		So(err, ShouldNotBeNil)

		v, err = NewConjunctionID(255, -1)
		So(v.Idx(), ShouldEqual, 255)
		So(v.DocID(), ShouldEqual, -1)

		v, err = NewConjunctionID(255, MaxDocumentID)
		So(v.Idx(), ShouldEqual, 255)
		So(v.DocID(), ShouldEqual, MaxDocumentID)

		v, err = NewConjunctionID(255, -MaxDocumentID)
		So(v.Idx(), ShouldEqual, 255)
		So(v.DocID(), ShouldEqual, -MaxDocumentID)

		_, err = NewConjunctionID(255, MaxDocumentID+1)
		So(err, ShouldNotBeNil)

		_, err = NewConjunctionID(255, -MaxDocumentID-1)
		So(err, ShouldNotBeNil)
	})
}
