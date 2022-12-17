package parser

import (
	"encoding/json"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestCommonNumberParser_ParseValue(t *testing.T) {
	parser := NewNumberParser()
	convey.Convey("test value", t, func() {
		r, e := parser.ParseValue([]int{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]int8{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]int16{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]int32{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]int64{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue([]uint{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]uint32{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]uint64{1, 2, 3})
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue([]string{"1", "2.0", "3"})
		convey.So(e, convey.ShouldBeNil)
		r, e = parser.ParseValue([]json.Number{"1", "2.0", "3"})
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue(12)
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue(int64(12))
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue(int32(12))
		convey.So(e, convey.ShouldBeNil)

		r, e = parser.ParseValue(float64(12.01))
		convey.So(e, convey.ShouldBeNil)
		convey.So(r, convey.ShouldResemble, []uint64{12})

		r, e = parser.ParseValue(json.Number("12"))
		convey.So(e, convey.ShouldBeNil)
		convey.So(r, convey.ShouldResemble, []uint64{12})
		r, e = parser.ParseValue(json.Number("12.01"))
		convey.So(e, convey.ShouldBeNil)
		convey.So(r, convey.ShouldResemble, []uint64{12})

		// not supported case
		r, e = parser.ParseValue([3]int64{1, 2, 3})
		convey.So(e, convey.ShouldNotBeNil)
		r, e = parser.ParseValue([3]string{"gonghuan", "hello", "1"})
		convey.So(e, convey.ShouldNotBeNil)
		r, e = parser.ParseValue([]string{"gonghuan", "hello"})
		convey.So(e, convey.ShouldNotBeNil)
		r, e = parser.ParseValue("gonghuan")
		convey.So(e, convey.ShouldNotBeNil)
	})
}
