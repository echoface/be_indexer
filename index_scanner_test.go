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

	convey.Convey("test skip to", t, func() {
		scg := NewFieldCursor(
			NewEntriesCursor(NewQKey("", nil), []EntryID{17, 32, 37}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{17, 33}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{19, 60}),
			NewEntriesCursor(NewQKey("", nil), []EntryID{53, 54}),
		)
		scg.SkipTo(19)
		convey.So(scg.current, convey.ShouldEqual, &scg.cursorGroup[2])
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, 19)
		convey.So(scg.current.cursor, convey.ShouldEqual, 0)
	})

	convey.Convey("skipto test", t, func() {
		entries := []EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111}
		scanner := NewEntriesCursor(NewQKey("age", 2), entries)
		convey.So(scanner.SkipTo(0), convey.ShouldEqual, 1)
		convey.So(scanner.cursor, convey.ShouldEqual, 0)
		convey.So(scanner.curEID, convey.ShouldEqual, 1)

		convey.So(scanner.SkipTo(1), convey.ShouldEqual, 1)
		convey.So(scanner.SkipTo(3), convey.ShouldEqual, 3)

		fmt.Println("scg:", scanner)
		convey.So(scanner.SkipTo(10), convey.ShouldEqual, 10)
		fmt.Println("scg:", scanner)
		convey.So(scanner.cursor, convey.ShouldEqual, 3)

		convey.So(scanner.SkipTo(10), convey.ShouldEqual, 10)
		convey.So(scanner.cursor, convey.ShouldEqual, 3)

		convey.So(scanner.SkipTo(11), convey.ShouldEqual, 11)
		convey.So(scanner.SkipTo(16), convey.ShouldEqual, 22)

		convey.So(scanner.SkipTo(111), convey.ShouldEqual, 111)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-2)
		convey.So(scanner.SkipTo(1000), convey.ShouldEqual, NULLENTRY)

		scanner = NewEntriesCursor(NewQKey("age", 2), entries)
		convey.So(scanner.SkipTo(22), convey.ShouldEqual, 22)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-3)

		scanner = NewEntriesCursor(NewQKey("age", 2), entries)
		convey.So(scanner.SkipTo(23), convey.ShouldEqual, 111)
		convey.So(scanner.SkipTo(23), convey.ShouldEqual, 111)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-2)
	})
	convey.Convey("test SkipTo with only one element", t, func() {
		scg := NewFieldCursor(
			NewEntriesCursor(NewQKey("age", 0), []EntryID{28}),
			NewEntriesCursor(NewQKey("age", 10), []EntryID{28, 29}),
		)
		fmt.Println("scg:", scg.cursorGroup[0], scg.cursorGroup[1], *scg.current)
		scg.SkipTo(32)
		fmt.Println("scg:", scg.cursorGroup[0], scg.cursorGroup[1], *scg.current)
		convey.So(scg.ReachEnd(), convey.ShouldBeTrue)
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, NULLENTRY)
		for _, cs := range scg.cursorGroup {
			convey.So(cs.curEID, convey.ShouldEqual, NULLENTRY)
		}
	})

	convey.Convey("rand test verify", t, func() {
		var entries Entries
		for i := 0; i < 10000; i++ {
			entries = append(entries, EntryID(rand.Int63n(7000)))
		}
		sort.Sort(entries)
		for i := 0; i < 1000; i++ {
			scanner := NewEntriesCursor(NewQKey("ut", 0), entries)

			randV := EntryID(rand.Int63n(20000))

			result := scanner.SkipTo(randV)
			if randV > entries[len(entries)-1] {
				convey.So(result, convey.ShouldEqual, NULLENTRY)
				convey.So(scanner.curEID, convey.ShouldEqual, NULLENTRY)
				convey.So(scanner.cursor, convey.ShouldBeGreaterThanOrEqualTo, len(entries))
			} else { // <= last value
				convey.So(entries[scanner.cursor] >= randV, convey.ShouldBeTrue)
				if scanner.cursor > 0 {
					convey.So(entries[scanner.cursor-1] < randV, convey.ShouldBeTrue)
				}
			}
		}
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
		current:     &cursor2,
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
