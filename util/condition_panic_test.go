package util

import (
	"fmt"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestPanicIf(t *testing.T) {
	convey.Convey("panic if err", t, func() {

		convey.So(func() {
			PanicIf(true, "should panic here")
		}, convey.ShouldPanic)

		convey.So(func() {
			PanicIf(false, "should not panic here")
		}, convey.ShouldNotPanic)
	})

}

func TestPanicIfErr(t *testing.T) {
	convey.Convey("panic if err", t, func() {

		convey.So(func() {
			PanicIfErr(fmt.Errorf("panic"), "should panic here")
		}, convey.ShouldPanic)

		convey.So(func() {
			PanicIfErr(nil, "should not panic here")
		}, convey.ShouldNotPanic)

		convey.So(func() {
			var err error
			PanicIfErr(err, "should not panic here")
		}, convey.ShouldNotPanic)
	})
}
