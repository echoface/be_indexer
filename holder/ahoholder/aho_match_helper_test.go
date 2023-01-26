package ahoholder

import (
	"fmt"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestBuildAcMatchContent(t *testing.T) {
	convey.Convey("test match gen content", t, func() {
		v, err := BuildAcMatchContent("abc", "#")
		convey.So(err, convey.ShouldBeNil)
		fmt.Println(v)

		v, err = BuildAcMatchContent([]string{"abc", "ddd", "eee"}, "#")
		convey.So(err, convey.ShouldBeNil)
		convey.So(string(v), convey.ShouldEqual, "abc#ddd#eee")

		v, err = BuildAcMatchContent([]interface{}{1, 2, 3}, "#")
		convey.So(err, convey.ShouldNotBeNil)
	})
}
