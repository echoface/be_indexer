package be_indexer

import (
	"fmt"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestEntriesCursor_SkipTo(t *testing.T) {
	scg := FieldCursor{
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
	scg := FieldCursor{
		current: nil,
		cursorGroup: CursorGroup{
			{
				key:     newQKey("age", 0),
				cursor:  0,
				entries: []EntryID{28},
			},
			{
				key:     newQKey("age", 10),
				cursor:  0,
				entries: []EntryID{28, 29},
			},
		},
	}
	scg.current = scg.cursorGroup[0]

	fmt.Println(scg.DumpEntries())
	convey.Convey("test SkipTo with only one element", t, func() {
		scg.SkipTo(32)
		convey.So(scg.current.cursor, convey.ShouldEqual, 1)
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, NULLENTRY)
	})
}

func TestQKey_String(t *testing.T) {
	vs := NewStrValues("红包", "跳蚤")
	for _, v := range vs {
		k := newQKey("age", v)
		fmt.Println(k.String())
	}
}
