package rangeholder

import (
	"github.com/echoface/be_indexer/core"
	"testing"

	. "github.com/echoface/be_indexer"
	"github.com/smartystreets/goconvey/convey"
)

func TestOptimizedRangeHolder_Basic(t *testing.T) {
	convey.Convey("Test OptimizedRangeHolder basic functionality", t, func() {
		builder := NewOptimizedRangeBuilder()
		builder.EnableDebug(true)

		convey.Convey("Test coordinate compression", func() {
			builder.compressor.AddValue(18)
			builder.compressor.AddValue(65)
			builder.compressor.AddValue(25)
			builder.compressor.AddValue(35)

			builder.compressor.Build()

			convey.So(builder.compressor.Size(), convey.ShouldEqual, 4)

			idx, ok := builder.compressor.GetIdx(18)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 0)

			idx, ok = builder.compressor.GetIdx(25)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 1)
		})

		convey.Convey("Test range insertion and query", func() {
			builder := NewOptimizedRangeBuilder()

			builder.pendingRanges = []pendingRange{
				{left: 18, right: 65, eid: core.EntryID(1)},
				{left: 25, right: 35, eid: core.EntryID(2)},
				{left: 60, right: 100, eid: core.EntryID(3)},
			}

			for _, pr := range builder.pendingRanges {
				builder.compressor.AddValue(pr.left)
				builder.compressor.AddValue(pr.right)
			}

			h, err := builder.CompileEntries()
			convey.So(err, convey.ShouldBeNil)
			holder := h.(*OptimizedRangeIndex)

			field := &core.FieldDesc{Field: "age"}
			result, err := holder.GetEntries(field, []int{30})

			convey.So(err, convey.ShouldBeNil)
			convey.So(result, convey.ShouldNotBeNil)
		})
	})
}

func TestCoordinateCompressor(t *testing.T) {
	convey.Convey("Test CoordinateCompressor", t, func() {
		cc := NewCoordinateCompressor()

		convey.Convey("Build and query", func() {
			cc.AddValue(100)
			cc.AddValue(200)
			cc.AddValue(50)
			cc.AddValue(100)

			cc.Build()

			convey.So(cc.Size(), convey.ShouldEqual, 3)

			idx, ok := cc.GetIdx(50)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 0)

			idx, ok = cc.GetIdx(100)
			convey.So(ok, convey.ShouldBeTrue)
			convey.So(idx, convey.ShouldEqual, 1)

			_, ok = cc.GetIdx(150)
			convey.So(ok, convey.ShouldBeFalse)
		})
	})
}

func TestOptimizedRangeHolder_LT_GT_Issue_E2E(t *testing.T) {
	convey.Convey("Test OptimizedRangeHolder LT/GT support via Indexer", t, func() {
		// Document 1: age < 30
		doc1 := core.NewDocument(1)
		doc1.AddConjunction(core.NewConjunction().LessThan("age", 30))

		// Document 2: age > 50
		doc2 := core.NewDocument(2)
		doc2.AddConjunction(core.NewConjunction().GreaterThan("age", 50))

		// Document 3: age between 40 and 60
		doc3 := core.NewDocument(3)
		doc3.AddConjunction(core.NewConjunction().Between("age", 40, 60))

		builder := NewCompactIndexerBuilder()
		// Use optimized_range holder
		builder.ConfigField("age", core.FieldOption{Container: "optimized_range"})

		err := builder.AddDocument(doc1, doc2, doc3)
		convey.So(err, convey.ShouldBeNil)

		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		convey.Convey("Query LT match (age=10, expect doc 1)", func() {
			ids, err := indexer.Retrieve(core.Assignments{"age": 10})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ids, convey.ShouldContain, core.DocID(1))
			convey.So(ids, convey.ShouldNotContain, core.DocID(2))
			convey.So(ids, convey.ShouldNotContain, core.DocID(3))
		})

		convey.Convey("Query GT match (age=100, expect doc 2)", func() {
			ids, err := indexer.Retrieve(core.Assignments{"age": 100})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ids, convey.ShouldContain, core.DocID(2))
			convey.So(ids, convey.ShouldNotContain, core.DocID(1))
			convey.So(ids, convey.ShouldNotContain, core.DocID(3))
		})

		convey.Convey("Query Between match (age=45, expect doc 3)", func() {
			ids, err := indexer.Retrieve(core.Assignments{"age": 45})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ids, convey.ShouldContain, core.DocID(3))
			convey.So(ids, convey.ShouldNotContain, core.DocID(1))
			convey.So(ids, convey.ShouldNotContain, core.DocID(2))
		})

		convey.Convey("Query Edge case match (age=55, expect doc 2 & 3)", func() {
			// 55 > 50 (doc 2) AND 40 <= 55 < 60 (doc 3)
			ids, err := indexer.Retrieve(core.Assignments{"age": 55})
			convey.So(err, convey.ShouldBeNil)
			convey.So(ids, convey.ShouldContain, core.DocID(2))
			convey.So(ids, convey.ShouldContain, core.DocID(3))
			convey.So(ids, convey.ShouldNotContain, core.DocID(1))
		})

		convey.Convey("Query No match (age=35)", func() {
			ids, err := indexer.Retrieve(core.Assignments{"age": 35})
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(ids), convey.ShouldEqual, 0)
		})
	})
}

func TestOptimizedRangeHolder_SerializeDeserialize_FlatEQ(t *testing.T) {
	convey.Convey("Serialize/Deserialize should keep EQ postings in flat form", t, func() {
		hBuilder := NewOptimizedRangeBuilder()
		// Prepare EQ postings (sorted EntryIDs)
		hBuilder.eqEntries[10] = core.Entries{core.EntryID(1), core.EntryID(5)}
		hBuilder.eqEntries[20] = core.Entries{core.EntryID(7)}

		// Prepare a range that does not affect EQ queries
		hBuilder.pendingRanges = []pendingRange{{left: 200, right: 300, eid: core.EntryID(9)}}
		hBuilder.compressor.AddValue(200)
		hBuilder.compressor.AddValue(300)

		h, err := hBuilder.CompileEntries()
		convey.So(err, convey.ShouldBeNil)
		holder := h.(*OptimizedRangeIndex)

		data, err := holder.Serialize()
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(data), convey.ShouldBeGreaterThan, 0)

		h2 := NewOptimizedRangeIndex()
		err = h2.Deserialize(data)
		convey.So(err, convey.ShouldBeNil)

		field := &core.FieldDesc{Field: "age"}

		convey.Convey("EQ query should return same postings", func() {
			cursors, err := h2.GetEntries(field, []int{10})
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(cursors), convey.ShouldEqual, 1)

			cur := cursors[0]
			var got core.Entries
			eid := cur.Current()
			for !eid.IsNULLEntry() {
				got = append(got, eid)
				eid = cur.SkipTo(eid + 1)
			}
			convey.So(got, convey.ShouldResemble, core.Entries{core.EntryID(1), core.EntryID(5)})
		})

		convey.Convey("Range query should still work", func() {
			cursors, err := h2.GetEntries(field, []int{250})
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(cursors), convey.ShouldEqual, 1)

			cur := cursors[0]
			var got core.Entries
			eid := cur.Current()
			for !eid.IsNULLEntry() {
				got = append(got, eid)
				eid = cur.SkipTo(eid + 1)
			}
			convey.So(got, convey.ShouldResemble, core.Entries{core.EntryID(9)})
		})
	})
}
