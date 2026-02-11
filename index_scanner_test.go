package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/roaring64"

	"github.com/smartystreets/goconvey/convey"
)

func TestEntriesCursor_SkipTo(t *testing.T) {

	convey.Convey("test skip to", t, func() {
		scg := NewFieldCursor(
			NewSliceIterator(NewTerm("", nil), []core.EntryID{17, 32, 37}),
			NewSliceIterator(NewTerm("", nil), []core.EntryID{17, 33}),
			NewSliceIterator(NewTerm("", nil), []core.EntryID{19, 60}),
			NewSliceIterator(NewTerm("", nil), []core.EntryID{53, 54}),
		)
		scg.SkipTo(19)
		convey.So(scg.current, convey.ShouldEqual, scg.cursorGroup[2])
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, 19)
		cur, ok := scg.current.(*SliceIterator)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(cur.cursor, convey.ShouldEqual, 0)
	})

	convey.Convey("skipto test", t, func() {
		entries := []core.EntryID{1, 2, 3, 10, 10, 10, 11, 12, 15, 15, 22, 111, 111}
		scanner := NewSliceIterator(NewTerm("age", 2), entries)
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
		convey.So(scanner.SkipTo(1000), convey.ShouldEqual, core.NULLENTRY)

		scanner = NewSliceIterator(NewTerm("age", 2), entries)
		convey.So(scanner.SkipTo(22), convey.ShouldEqual, 22)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-3)

		scanner = NewSliceIterator(NewTerm("age", 2), entries)
		convey.So(scanner.SkipTo(23), convey.ShouldEqual, 111)
		convey.So(scanner.SkipTo(23), convey.ShouldEqual, 111)
		convey.So(scanner.cursor, convey.ShouldEqual, len(scanner.entries)-2)
	})
	convey.Convey("test SkipTo with only one element", t, func() {
		scg := NewFieldCursor(
			NewSliceIterator(NewTerm("age", 0), []core.EntryID{28}),
			NewSliceIterator(NewTerm("age", 10), []core.EntryID{28, 29}),
		)
		fmt.Println("scg:", scg.cursorGroup[0], scg.cursorGroup[1], scg.current)
		scg.SkipTo(32)
		fmt.Println("scg:", scg.cursorGroup[0], scg.cursorGroup[1], scg.current)
		convey.So(scg.ReachEnd(), convey.ShouldBeTrue)
		convey.So(scg.GetCurEntryID(), convey.ShouldEqual, core.NULLENTRY)
		for _, cs := range scg.cursorGroup {
			raw, ok := cs.(*SliceIterator)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(raw.curEID, convey.ShouldEqual, core.NULLENTRY)
		}
	})

	convey.Convey("rand test verify", t, func() {
		var entries core.Entries
		for i := 0; i < 10000; i++ {
			entries = append(entries, core.EntryID(rand.Int63n(7000)))
		}
		sort.Sort(entries)
		for i := 0; i < 1000; i++ {
			scanner := NewSliceIterator(NewTerm("ut", 0), entries)

			randV := core.EntryID(rand.Int63n(20000))

			result := scanner.SkipTo(randV)
			if randV > entries[len(entries)-1] {
				convey.So(result, convey.ShouldEqual, core.NULLENTRY)
				convey.So(scanner.curEID, convey.ShouldEqual, core.NULLENTRY)
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

func TestRoaringIterator_SkipTo(t *testing.T) {
	convey.Convey("test roaring iterator skip to", t, func() {
		bm := roaring64.New()
		entries := []uint64{1, 2, 3, 10, 11, 12, 15, 22, 111}
		for _, v := range entries {
			bm.Add(v)
		}

		iter := NewRoaringIterator(NewTerm("age", 1), bm)

		convey.So(iter.Current(), convey.ShouldEqual, 1)
		
		// Skip to existing
		convey.So(iter.SkipTo(3), convey.ShouldEqual, 3)
		convey.So(iter.Current(), convey.ShouldEqual, 3)

		// Skip to non-existing (gap)
		convey.So(iter.SkipTo(5), convey.ShouldEqual, 10)
		convey.So(iter.Current(), convey.ShouldEqual, 10)

		// Skip to same
		convey.So(iter.SkipTo(10), convey.ShouldEqual, 10)

		// Skip to far
		convey.So(iter.SkipTo(100), convey.ShouldEqual, 111)

		// Skip to end
		convey.So(iter.SkipTo(200), convey.ShouldEqual, core.NULLENTRY)
		convey.So(iter.Current(), convey.ShouldEqual, core.NULLENTRY)
	})

	convey.Convey("test roaring iterator empty", t, func() {
		bm := roaring64.New()
		iter := NewRoaringIterator(NewTerm("age", 1), bm)
		convey.So(iter.Current(), convey.ShouldEqual, core.NULLENTRY)
		convey.So(iter.SkipTo(10), convey.ShouldEqual, core.NULLENTRY)
	})
}
