package rangeholder

import (
	"fmt"
	"math"
	"testing"

	. "github.com/echoface/be_indexer"
	"github.com/smartystreets/goconvey/convey"
)

func TestRangeEntries_Clone(t *testing.T) {
	convey.Convey("test clone", t, func() {
		v := NewRangeEntries(0, 100)
		v.entries = Entries{1, 2, 3, 4, 4, 5, 5, 888, 111, 999, 1111, 1000, 20000}
		v2 := v.Clone()
		convey.So(v.left, convey.ShouldEqual, v2.left)
		convey.So(v.right, convey.ShouldEqual, v2.right)
		convey.So(v.entries, convey.ShouldResemble, v2.entries)
	})
	convey.Convey("test explode", t, func() {
		v := NewRangeEntries(0, 100)
		v.entries = Entries{1, 2, 3, 4, 4}
		convey.Convey("out of range", func() {
			convey.So(func() { v.Explode(-1, 99) }, convey.ShouldPanic)
			convey.So(func() { v.Explode(0, 101) }, convey.ShouldPanic)
			convey.So(func() { v.Explode(-1, 101) }, convey.ShouldPanic)
			convey.So(func() { v.Explode(-2, -1) }, convey.ShouldPanic)
			convey.So(func() { v.Explode(101, 102) }, convey.ShouldPanic)
			convey.So(func() { v.Explode(100, 100) }, convey.ShouldPanic)

			convey.So(func() { v.Explode(1, 99) }, convey.ShouldNotPanic)
			convey.So(func() { v.Explode(0, 99) }, convey.ShouldNotPanic)
			convey.So(func() { v.Explode(0, 100) }, convey.ShouldNotPanic)
			convey.So(func() { v.Explode(1, 100) }, convey.ShouldNotPanic)
		})
		convey.Convey("range split", func() {
			rgs := v.Explode(0, 100)
			fmt.Println("explore(0,100):", rgs)

			rgs = v.Explode(20, 50)
			fmt.Println("explore(20,50):", rgs)

			rgs = v.Explode(20, 100)
			fmt.Println("explore(20,100):", rgs)

			rgs = v.Explode(0, 50)
			fmt.Println("explore(0,50):", rgs)

			rgs = v.Explode(50, 50)
			fmt.Println("explore(50,50):", rgs)

			rgs = v.Explode(0, 0)
			fmt.Println("explore(0, 0):", rgs)
			rgs = v.Explode(1, 1)
			fmt.Println("explore(1, 1):", rgs)
			rgs = v.Explode(99, 99)
			fmt.Println("explore(99,99):", rgs)

			rg2 := Range{1, 2}
			rgs = rg2.Explode(1, 1)
			fmt.Printf("%v explore(1, 1):%v\n", rg2, rgs)
		})
	})
}

func TestRangeIdx(t *testing.T) {
	// convey.Convey("test clone", t, func() {
	idx := NewRangeIdx(math.MinInt64, math.MaxInt64)
	idx.IndexingRange(1, 1, 100)
	fmt.Println(idx.String())
	idx.IndexingRange(1, 1, 101)
	fmt.Println(idx.String())
	idx.IndexingRange(-5, 3, 102)
	fmt.Println(idx.String())

	idx.IndexingRange(1, math.MaxInt64, 105)
	fmt.Println(idx.String())

	idx.IndexingRange(100, 1000, 108)
	fmt.Println(idx.String())

	idx.IndexingRange(4, 99, 109)
	fmt.Println(idx.String())

	idx.Compile()
	rgPl := idx.Retrieve(5)
	fmt.Println(rgPl.String())
	//})
}

func TestBEIndex_WithExtendRange(t *testing.T) {
	LogLevel = DebugLevel

	// 12:
	// (sex is man && age > 18)
	// or
	// (sex is female && age < 20)
	doc := NewDocument(12)
	doc.AddConjunction(
		NewConjunction().In("sex", "man").GreaterThan("age", 18),
		NewConjunction().In("sex", "female").LessThan("age", 20),
	)

	doc2 := NewDocument(13)
	doc2.AddConjunction(
		NewConjunction().NotIn("sex", NewStrValues("man", "female")).Between("age", 0, 25),
	)

	builder := NewCompactIndexerBuilder(WithBadConjBehavior(SkipBadConj))
	builder.ConfigField("age", FieldOption{Container: HolderNameExtendRange})
	_ = builder.AddDocument(doc, doc2)
	indexer := builder.BuildIndex()
	PrintIndexEntries(indexer)

	type Case struct {
		age    []int
		sex    string
		expect DocIDList
	}
	cases := []*Case{
		//{age: []int{20}, sex: "man", expect: DocIDList{12}},
		//{age: []int{20}, sex: "female", expect: DocIDList{}},
		{age: []int{20}, sex: "other", expect: DocIDList{13}},
		//{age: []int{25}, sex: "other", expect: DocIDList{}},
	}
	convey.Convey("run cases", t, func() {
		for _, cs := range cases {
			ids, err := indexer.Retrieve(Assignments{
				"age": cs.age,
				"sex": cs.sex,
			}, WithDumpEntries(), WithStepDetail())
			fmt.Println("cs:", cs, ", result:", ids)
			convey.So(err, convey.ShouldBeNil)
			if len(ids) > 0 || len(cs.expect) > 0 {
				convey.So(ids, convey.ShouldResemble, cs.expect)
			}
		}
	})
}
