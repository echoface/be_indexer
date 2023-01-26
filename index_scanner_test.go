package be_indexer

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/roaring64"

	"github.com/smartystreets/goconvey/convey"
)

func TestEntriesCursor_SkipTo(t *testing.T) {
	scg := FieldCursor{
		current: nil,
		cursorGroup: EntriesCursors{
			NewEntriesCursor(NewQKey("", nil), []EntryID{17, 32, 37}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{17, 33}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{19, 60}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{53, 54}),
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
		cursorGroup: EntriesCursors{
			NewEntriesCursor(NewQKey("age", 0), []EntryID{28}),
			NewEntriesCursor(NewQKey("age", 10), []EntryID{28, 29}),
		},
	}
	scg.current = scg.cursorGroup[0]

	sb := &strings.Builder{}
	scg.DumpEntries(sb)
	fmt.Println(sb.String())
	convey.Convey("test SkipTo with only one element", t, func() {
		scg.SkipTo(32)
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, NULLENTRY)
		convey.So(scg.current.cursor, convey.ShouldEqual, 1)
	})
}

func TestEntriesCursor_DumpEntries(t *testing.T) {
	cursor := NewEntriesCursor(NewQKey("age", 18), nil)
	cursor2 := NewEntriesCursor(NewQKey("age", 25), nil)
	testIDCnt := 20
	for i := 0; i < testIDCnt-1; i++ {
		conjID := NewConjID(DocID(i), rand.Intn(3), 3)
		cursor.entries = append(cursor.entries, NewEntryID(conjID, rand.Intn(10) < 5))
		if i%2 == 0 {
			cursor2.entries = append(cursor2.entries, NewEntryID(conjID, rand.Intn(10) < 5))
		}
	}
	cursor.entries = append(cursor.entries, NULLENTRY)
	cursor.idSize = len(cursor.entries)
	cursor2.entries = append(cursor2.entries, NULLENTRY)
	cursor2.idSize = len(cursor2.entries)

	sort.Sort(cursor.entries)

	for cur := 0; cur < testIDCnt; cur++ {
		cursor.cursor = cur
		sb := &strings.Builder{}
		cursor.DumpEntries(sb)
		fmt.Println(sb.String())
	}

	cursor.cursor = testIDCnt / 2
	fc := &FieldCursor{
		current:     cursor2,
		cursorGroup: EntriesCursors{cursor, cursor2},
	}

	sb := &strings.Builder{}
	fc.DumpEntries(sb)

	fmt.Println(sb.String())
}

func TestDocIDCollector_Add(t *testing.T) {
	ids := make([]int64, 0, 10000)
	mapCost := make([]int64, 0, 10)
	bitCost := make([]int64, 0, 10)

	bits := roaring64.New()
	m := map[int64]struct{}{}

	for _, cnt := range []int{10, 1000, 10000, 100000} {
		for len(ids) < cnt {
			ids = append(ids, rand.Int63n(1000000))
		}
		start := time.Now().UnixNano() / 1000
		for _, v := range ids {
			m[v] = struct{}{}
		}
		end := time.Now().UnixNano() / 1000
		mapCost = append(mapCost, end-start)

		start = time.Now().UnixNano() / 1000
		for _, v := range ids {
			bits.Add(uint64(v))
		}
		end = time.Now().UnixNano() / 1000
		bitCost = append(bitCost, end-start)

		bits.Clear()
		for k := range m {
			delete(m, k)
		}
	}
	fmt.Println("mapcost:", mapCost)
	fmt.Println("bitcost:", bitCost)
}
