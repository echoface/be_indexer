package rangeholder

import (
	"testing"

	. "github.com/echoface/be_indexer"
	"github.com/smartystreets/goconvey/convey"
)

func TestOptimizedRangeHolder_Basic(t *testing.T) {
	convey.Convey("Test OptimizedRangeHolder basic functionality", t, func() {
		holder := NewOptimizedRangeHolder()
		holder.EnableDebug(true)

		convey.Convey("Test coordinate compression", func() {
			holder.compressor.AddValue(18)
			holder.compressor.AddValue(65)
			holder.compressor.AddValue(25)
			holder.compressor.AddValue(35)

			holder.compressor.Build()

			convey.So(holder.compressor.Size(), convey.ShouldEqual, 4)

			idx, ok := holder.compressor.GetIdx(18)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 0)

			idx, ok = holder.compressor.GetIdx(25)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 1)
		})

		convey.Convey("Test range insertion and query", func() {
			holder := NewOptimizedRangeHolder()

			holder.pendingRanges = []pendingRange{
				{left: 18, right: 65, eid: EntryID(1)},
				{left: 25, right: 35, eid: EntryID(2)},
				{left: 60, right: 100, eid: EntryID(3)},
			}

			for _, pr := range holder.pendingRanges {
				holder.compressor.AddValue(pr.left)
				holder.compressor.AddValue(pr.right)
			}

			err := holder.CompileEntries()
			convey.So(err, convey.ShouldBeNil)

			field := &FieldDesc{Field: "age"}
			result, err := holder.GetEntries(field, []int{30})

			convey.So(err, convey.ShouldBeNil)
			convey.So(result, convey.ShouldNotBeNil)
		})
	})
}

func TestCoordinateCompressor(t *testing.T) {
	convey.Convey("Test CoordinateCompressor", t, func() {
		cc := NewCoordinateCompressor()

		convey.Convey("Build and query", func() {
			cc.AddValue(100)
			cc.AddValue(200)
			cc.AddValue(50)
			cc.AddValue(100)

			cc.Build()

			convey.So(cc.Size(), convey.ShouldEqual, 3)

			idx, ok := cc.GetIdx(50)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 0)

			idx, ok = cc.GetIdx(100)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 1)

			_, ok = cc.GetIdx(150)
			convey.So(ok, convey.ShouldBeFalse)
		})
	})
}
