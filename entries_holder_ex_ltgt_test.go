package be_indexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestLGTHolder(t *testing.T) {
	convey.Convey("test holder index expression", t, func() {
		holder := NewDefaultExtLtGtHolder(true)
		field := &FieldDesc{
			FieldOption: FieldOption{Parser: nil, Container: "ut"},
			ID:          0,
			Field:       "age",
		}
		for i := 0; i < 10; i++ {
			v := NewBoolValue(ValueOptEQ, []int{10, i%3 + 5}, true)
			data, e := holder.IndexingBETx(field, &v)
			convey.So(e, convey.ShouldBeNil)
			tx := IndexingBETx{holder: holder, field: field, data: data, eid: NewEntryID(ConjID(i+100), true)}
			e = holder.CommitIndexingBETx(tx)
			convey.So(e, convey.ShouldBeNil)

			v = NewGTBoolValue(int64(i%3 + 5))
			data, e = holder.IndexingBETx(field, &v)
			convey.So(e, convey.ShouldBeNil)
			tx = IndexingBETx{holder: holder, field: field, data: data, eid: NewEntryID(ConjID(i+50), true)}
			e = holder.CommitIndexingBETx(tx)
			convey.So(e, convey.ShouldBeNil)

			v = NewLTBoolValue(int64(i%3 + 5))
			data, e = holder.IndexingBETx(field, &v)
			convey.So(e, convey.ShouldBeNil)
			tx = IndexingBETx{holder: holder, field: field, data: data, eid: NewEntryID(ConjID(i+10), true)}
			e = holder.CommitIndexingBETx(tx)
			convey.So(e, convey.ShouldBeNil)
		}
		convey.So(holder.CompileEntries(), convey.ShouldBeNil)

		sb := &strings.Builder{}
		holder.DumpEntries(sb)
		fmt.Println(sb.String())

		convey.Convey("lt retrieve", func() {
			plList, e := holder.GetEntries(field, []int64{-100, -1}) // only lt result retrieved
			convey.So(e, convey.ShouldBeNil)
			fmt.Println("0:result:", FieldCursors{NewFieldCursor(plList...)}.Dump())
			vs := make([]int64, 0, 0)
			for _, pl := range plList {
				vs = append(vs, pl.key.value.(int64))
				for _, eid := range pl.entries {
					convey.So(eid.GetConjID(), convey.ShouldBeBetweenOrEqual, 10, 20)
				}
			}
			convey.So(vs, convey.ShouldResemble, []int64{7, 6, 5}) // reverse order
		})

		convey.Convey("gt retrieve", func() {
			plList, e := holder.GetEntries(field, []int64{100, 200}) // only lt result retrieved
			convey.So(e, convey.ShouldBeNil)
			fmt.Println("100,200:result:", FieldCursors{NewFieldCursor(plList...)}.Dump())
			vs := make([]int64, 0, 0)
			for _, pl := range plList {
				vs = append(vs, pl.key.value.(int64))
				for _, eid := range pl.entries {
					convey.So(eid.GetConjID(), convey.ShouldBeBetweenOrEqual, 50, 60)
				}
			}
			convey.So(vs, convey.ShouldResemble, []int64{5, 6, 7}) // reverse order
		})

		convey.Convey("gt-lt-kv both retrieve", func() {
			plList, e := holder.GetEntries(field, []int64{6}) // only lt result retrieved
			convey.So(e, convey.ShouldBeNil)
			convey.So(len(plList), convey.ShouldEqual, 3)
			fmt.Println("6:result:", FieldCursors{NewFieldCursor(plList...)}.Dump())
			for _, pl := range plList {
				v := pl.key.value.(int64)
				switch v {
				case 5: // should be 6 ">5" retrieved(greater than expression)
					for _, eid := range pl.entries {
						convey.So(eid.GetConjID(), convey.ShouldBeBetweenOrEqual, 50, 60)
					}
				case 6: // should be "=6" retrieved(in/not in expression)
					for _, eid := range pl.entries {
						convey.So(eid.GetConjID(), convey.ShouldBeBetweenOrEqual, 100, 110)
					}
				case 7: // should be 6 "<7" retrieved(less than expression)
					for _, eid := range pl.entries {
						convey.So(eid.GetConjID(), convey.ShouldBeBetweenOrEqual, 10, 20)
					}
				}
			}
		})
	})
}
