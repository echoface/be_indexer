package be_indexer

import (
	"github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestEntriesCursor_SkipTo(t *testing.T) {
	scg := FieldScanner{
		current: nil,
		cursorGroup: CursorGroup{
			{
				cursor:  0,
				entries: []EntryID{17, 32, 37},
			},
			{
				cursor:  0,
				entries: []EntryID{17, 33},
			},
			{
				cursor:  0,
				entries: []EntryID{19, 60},
			},
			{
				cursor:  0,
				entries: []EntryID{53, 54},
			},
		},
	}
	scg.current = scg.cursorGroup[0]

	convey.Convey("test skip to", t, func() {
		scg.SkipTo(19)

		convey.So(scg.current, convey.ShouldResemble, scg.cursorGroup[2])
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, 19)
		convey.So(scg.current.cursor, convey.ShouldEqual, 0)
	})
}

func TestEntriesCursor_SkipTo2(t *testing.T) {
	scg := FieldScanner{
		current: nil,
		cursorGroup: CursorGroup{
			{
				cursor:  0,
				entries: []EntryID{28},
			},
			{
				cursor:  0,
				entries: []EntryID{28},
			},
		},
	}
	scg.current = scg.cursorGroup[0]

	convey.Convey("test SkipTo with only one element", t, func() {
		scg.SkipTo(29)
		convey.So(scg.current.cursor, convey.ShouldEqual, 1)
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, NULLENTRY)
	})
}
