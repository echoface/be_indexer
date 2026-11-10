package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/core"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestEntries_Less(t *testing.T) {
	convey.Convey("test entries sort", t, func() {
		ent := core.Entries{10, 1, 2, 3, 4, 1, 8, 9, 10}
		sort.Sort(ent)
		fmt.Println(ent)
		convey.So(ent, convey.ShouldResemble, core.Entries{1, 1, 2, 3, 4, 8, 9, 10, 10})
	})
}

func TestNewConjID(t *testing.T) {
	convey.Convey("test conjunction id", t, func() {
		cases := []struct {
			docID       core.DocID
			index, size int
		}{
			{0, 0, 0},
			{1, 2, 3},
			{2, 0, 1},
			{12, 1, 20},
			{-111, 1, 20},
			{core.MaxDocID, 255, 255},
			{-core.MaxDocID, 255, 255},
		}
		for _, cs := range cases {
			id := core.NewConjID(cs.docID, cs.index, cs.size)
			convey.So(id.DocID(), convey.ShouldEqual, cs.docID)
			convey.So(id.Index(), convey.ShouldEqual, cs.index)
			convey.So(id.IncludeCount(), convey.ShouldEqual, cs.size)
		}
	})
}
