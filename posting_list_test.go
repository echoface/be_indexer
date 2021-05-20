package be_indexer

import (
	"fmt"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestPostingList_Skip(t *testing.T) {
	convey.Convey("skip test", t, func() {
		sc := EntriesCursor{
			key:     NewKey(1, 2),
			cursor:  0,
			entries: []EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111},
		}
		convey.So(sc.Skip(0), convey.ShouldEqual, 1)

		convey.So(sc.Skip(1), convey.ShouldEqual, 2)
		fmt.Println("skip test finish, 01")
		convey.So(sc.Skip(10), convey.ShouldEqual, 11)
		fmt.Println("skip test finish, 02")
		convey.So(sc.Skip(15), convey.ShouldEqual, 22)
		fmt.Println("skip test finish, 03")
		convey.So(sc.Skip(111), convey.ShouldEqual, NULLENTRY)
		fmt.Println("skip test finish, 04")
		convey.So(sc.Skip(2), convey.ShouldEqual, NULLENTRY)
	})
	fmt.Println("skip test finish")

	convey.Convey("skip to test", t, func() {
		scanner := EntriesCursor{
			key:     NewKey(1, 2),
			cursor:  0,
			entries: []EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111},
		}
		convey.So(scanner.SkipTo(0), convey.ShouldEqual, 1)
		convey.So(scanner.SkipTo(1), convey.ShouldEqual, 1)
		convey.So(scanner.SkipTo(3), convey.ShouldEqual, 3)
		convey.So(scanner.SkipTo(10), convey.ShouldEqual, 10)
		convey.So(scanner.cursor, convey.ShouldEqual, 3)
		convey.So(scanner.SkipTo(10), convey.ShouldEqual, 10)
		convey.So(scanner.cursor, convey.ShouldEqual, 3)

		convey.So(scanner.SkipTo(11), convey.ShouldEqual, 11)
		convey.So(scanner.SkipTo(16), convey.ShouldEqual, 22)

		convey.So(scanner.SkipTo(111), convey.ShouldEqual, 111)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-2)
		convey.So(scanner.SkipTo(1000), convey.ShouldEqual, NULLENTRY)
	})
	fmt.Println("skipto test finish")
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

func TestPostingList_SkipTo(t *testing.T) {
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

	convey.Convey("test SkipTo", t, func() {
		scg.SkipTo(19)
		fmt.Println("current:", scg.current)
		fmt.Println("curEID:", scg.GetCurEntryID())
		fmt.Println("curent cursor:", scg.current.cursor)
	})
}

func TestPostingList_SkipTo2(t *testing.T) {
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
		fmt.Println("current:", scg.current)
		fmt.Println("curEID:", scg.GetCurEntryID())
		fmt.Println("curent cursor:", scg.current.cursor)
	})
}
