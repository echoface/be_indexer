package be_indexer

import (
	"fmt"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestEntries_Less(t *testing.T) {
	convey.Convey("test entries sort", t, func() {
		ent := Entries{10, 1, 2, 3, 4, 1, 8, 9, 10}
		sort.Sort(ent)
		fmt.Println(ent)
		convey.So(ent, convey.ShouldResemble, Entries{1, 1, 2, 3, 4, 8, 9, 10, 10})
	})
}

func TestNewConjID(t *testing.T) {
	convey.Convey("test conjunction id", t, func() {
		cases := [][3]int{
			{0, 0, 0},
			{1, 2, 3},
			{2, 0, 1},
			{12, 1, 20},
			{-111, 1, 20},
			{MaxDocID, 255, 255},
			{-MaxDocID, 255, 255},
		}
		for _, cs := range cases {
			id := NewConjID(DocID(cs[0]), cs[1], cs[2])
			convey.So(id.DocID(), convey.ShouldEqual, cs[0])
			convey.So(id.Index(), convey.ShouldEqual, cs[1])
			convey.So(id.Size(), convey.ShouldEqual, cs[2])
		}
	})
}
