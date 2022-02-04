package util

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestJSONPretty(t *testing.T) {
	convey.Convey("test json string", t, func() {

		s := JSONPretty(nil)
		convey.So(s, convey.ShouldEqual, "null")

		v := struct {
		}{}
		s = JSONPretty(v)
		convey.So(s, convey.ShouldEqual, "{}")
	})
}

func TestJSONString(t *testing.T) {
	convey.Convey("test json string", t, func() {
		s := JSONString(nil)
		convey.So(s, convey.ShouldEqual, "null")

		v := struct {
		}{}
		s = JSONString(v)
		convey.So(s, convey.ShouldEqual, "{}")
	})
}
