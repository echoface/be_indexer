package beindexer

import (
	"fmt"
	"github.com/smartystreets/goconvey/convey"
	"sort"
	"testing"
)

func TestPostingList_Skip(t *testing.T) {
	convey.Convey("skip test", t, func() {
		pl := PostingList{
			key:     NewKey(1, 2),
			cursor:  0,
			entries: []EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111},
		}
		convey.So(pl.Skip(0), convey.ShouldEqual, 1)
		convey.So(pl.Skip(1), convey.ShouldEqual, 2)
		convey.So(pl.Skip(10), convey.ShouldEqual, 11)
		convey.So(pl.Skip(15), convey.ShouldEqual, 22)
		convey.So(pl.Skip(111), convey.ShouldEqual, NULLENTRY)
		convey.So(pl.Skip(2), convey.ShouldEqual, NULLENTRY)
	})

	convey.Convey("skip to test", t, func() {
		pl := PostingList{
			key:     NewKey(1, 2),
			cursor:  0,
			entries: []EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111},
		}
		convey.So(pl.SkipTo(0), convey.ShouldEqual, 1)
		convey.So(pl.SkipTo(1), convey.ShouldEqual, 1)
		convey.So(pl.SkipTo(3), convey.ShouldEqual, 3)
		convey.So(pl.SkipTo(10), convey.ShouldEqual, 10)
		convey.So(pl.cursor, convey.ShouldEqual, 3)
		convey.So(pl.SkipTo(10), convey.ShouldEqual, 10)
		convey.So(pl.cursor, convey.ShouldEqual, 3)

		convey.So(pl.SkipTo(11), convey.ShouldEqual, 11)
		convey.So(pl.SkipTo(16), convey.ShouldEqual, 22)

		convey.So(pl.SkipTo(111), convey.ShouldEqual, 111)
		convey.So(pl.cursor, convey.ShouldEqual, len(pl.entries)-2)
		convey.So(pl.SkipTo(1000), convey.ShouldEqual, NULLENTRY)
	})
}

func TestEntries_Less(t *testing.T) {
	convey.Convey("test entries sort", t, func() {

		ent := Entries{10, 1, 2, 3, 4, 1, 8, 9, 10}
		sort.Sort(ent)
		fmt.Println(ent)
		convey.So(ent, convey.ShouldResemble, Entries{1, 1, 2, 3, 4, 8, 9, 10, 10})
	})
}

func TestEntries_Key(t *testing.T) {
	f := NewKey(MaxBEFieldID, MaxBEValueID)
	fmt.Printf("%x\n", f.GetFieldID())
	fmt.Printf("%x\n", f.GetValueID())
}
