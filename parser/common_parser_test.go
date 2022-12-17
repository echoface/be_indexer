package parser

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestCommonStrParser_ParseValue(t *testing.T) {
	parser := NewCommonStrParser()

	convey.Convey("test common parser", t, func() {
		r, e := parser.ParseValue([]int{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(r), convey.ShouldEqual, 3)

		r, e = parser.ParseValue([]uint8{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(r), convey.ShouldEqual, 3)

		r, e = parser.ParseValue([]string{"gonghuan", "hello"})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(r), convey.ShouldEqual, 2)

		r, e = parser.ParseValue("gonghuan")
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(r), convey.ShouldEqual, 1)

		r, e = parser.ParseValue(12)
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(r), convey.ShouldEqual, 1)

		r, e = parser.ParseValue([3]int64{1, 2, 3})
		convey.So(e, convey.ShouldNotBeNil)
	})

}
