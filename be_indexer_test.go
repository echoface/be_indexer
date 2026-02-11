package be_indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/echoface/be_indexer/core"

	"github.com/echoface/be_indexer/codegen/cache"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"github.com/smartystreets/goconvey/convey"
)

func buildTestDoc() []*core.Document {
	docs := make([]*core.Document, 0)
	content, e := os.ReadFile("./static/testdata/test_docs.json")
	util.PanicIfErr(e, "load test docs fail")

	if e = json.Unmarshal(content, &docs); e != nil {
		panic(e)
	}
	fmt.Println("total docs:", len(docs))
	return docs
}

func TestBEIndex_Retrieve(t *testing.T) {
	LogLevel = InfoLevel
	//RegisterEntriesHolder(HolderNameDefault, func() core.EntriesHolder {
	//	holder := NewDefaultEntriesHolder()
	//	holder.FieldParser = map[core.BEField]parser.FieldValueParser{
	//		"age": parser.NewNumberParser(),
	//	}
	//	return holder
	//})

	var err error
	builder := NewIndexerBuilder()

	for _, doc := range buildTestDoc() {
		err = builder.AddDocument(doc)
		util.PanicIfErr(err, "add document fail")
	}

	indexer, err := builder.BuildIndex()
	util.PanicIfErr(err, "build index failed")
	PrintIndexInfo(indexer)

	result, e := indexer.Retrieve(map[core.BEField]core.Values{
		"age": core.NewIntValues(5),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[core.BEField]core.Values{
		"ip": core.NewStrValues("localhost"),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[core.BEField]core.Values{
		"age":  core.NewIntValues(1),
		"city": core.NewStrValues("sh"),
		"tag":  core.NewStrValues("tag1"),
	})
	fmt.Println(e, result)
}

type MockTargeting struct {
	ID   core.DocID
	A    []int
	NegA bool
	B    []int
	NegB bool
	C    []int
	NegC bool
	D    []int
	NegD bool
}

type Q struct {
	A []int
	B []int
	C []int
	D []int
}

func (q *Q) ToAssigns() core.Assignments {
	assign := core.Assignments{}
	if len(q.A) > 0 {
		assign["A"] = q.A
	}
	if len(q.B) > 0 {
		assign["B"] = q.B
	}
	if len(q.C) > 0 {
		assign["C"] = q.C
	}
	if len(q.D) > 0 {
		assign["D"] = q.D
	}
	return assign
}

func (t *MockTargeting) ToConj() *core.Conjunction {
	conj := core.NewConjunction()
	if len(t.A) > 0 {
		if t.NegA {
			conj.NotIn("A", t.A)
		} else {
			conj.In("A", t.A)
		}
	}
	if len(t.B) > 0 {
		if t.NegB {
			conj.NotIn("B", t.B)
		} else {
			conj.In("B", t.B)
		}
	}
	if len(t.C) > 0 {
		if t.NegC {
			conj.NotIn("C", t.C)
		} else {
			conj.In("C", t.C)
		}
	}
	if len(t.D) > 0 {
		if t.NegD {
			conj.NotIn("D", t.D)
		} else {
			conj.In("D", t.D)
		}
	}
	return conj
}

func containAny(values, queries []int) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range queries {
		if util.ContainInt(values, v) {
			return true
		}
	}
	return false
}

func (t *MockTargeting) String() string {
	b, _ := json.Marshal(t)
	return string(b)
}

func (t *MockTargeting) ToDocument() *core.Document {
	conj := t.ToConj()
	if len(conj.Predicates) == 0 {
		return nil
	}
	doc := core.NewDocument(t.ID)
	doc.AddConjunction(conj)
	return doc
}

func (t *MockTargeting) Match(a, b, c, d []int) bool {
	hasA := containAny(t.A, a)
	if len(t.A) > 0 {
		if hasA && t.NegA {
			return false
		} else if !hasA && !t.NegA {
			return false
		}
	}

	if len(t.B) > 0 {
		hasB := containAny(t.B, b)
		if hasB && t.NegB {
			return false
		} else if !hasB && !t.NegB {
			return false
		}
	}

	if len(t.C) > 0 {
		hasC := containAny(t.C, c)
		if hasC && t.NegC {
			return false
		} else if !hasC && !t.NegC {
			return false
		}
	}

	if len(t.D) > 0 {
		hasD := containAny(t.D, d)
		if hasD && t.NegD {
			return false
		} else if !hasD && !t.NegD {
			return false
		}
	}
	return true
}

func randValue(cnt int) (res []int) {
	if cnt > 100 {
		cnt = 100
	}
	for cnt > len(res) {
		res = append(res, rand.Intn(150))
	}
	return util.DistinctInt(res)
}

func BuildTestDocumentAndQueries(docCnt, queriesCnt int, withNeg bool) (map[core.DocID]*MockTargeting, []*Q) {
	docs := make(map[core.DocID]*MockTargeting)
	for len(docs) < docCnt {
		target := &MockTargeting{
			ID: core.DocID(len(docs) + 1),
			A:  randValue(10),
			B:  randValue(20),
			C:  randValue(30),
			D:  randValue(40),
		}
		if withNeg {
			target.NegA = rand.Intn(100) > 50
			target.NegB = rand.Intn(100) > 50
			target.NegC = rand.Intn(100) > 50
			target.NegD = rand.Intn(100) > 50
		}

		if len(target.A)+len(target.B)+len(target.C)+len(target.D) > 0 {
			docs[target.ID] = target
		}
	}

	var assigns []*Q
	for queriesCnt > len(assigns) {
		q := &Q{
			// A: randValue(8),
			// B: randValue(6),
			// C: randValue(4),
			// D: randValue(2),
			A: randValue(2),
			B: randValue(2),
			C: randValue(2),
			D: randValue(2),
		}
		if len(q.A)+len(q.B)+len(q.C)+len(q.D) > 0 {
			assigns = append(assigns, q)
		}
	}
	return docs, assigns
}

func TestMatch(t *testing.T) {
	convey.Convey("test match", t, func() {
		d := &MockTargeting{
			ID:   62,
			C:    []int{43, 56, 77, 64, 5, 34, 7, 57},
			D:    []int{8, 24, 87, 71, 12, 4, 55},
			NegD: false,
		}
		query := &Q{
			A: nil,
			B: nil,
			C: []int{88, 43},
			D: []int{12, 4, 6},
		}
		convey.So(d.Match(query.A, query.B, query.C, query.D), convey.ShouldBeTrue)
	})

	convey.Convey("test neg match", t, func() {
		d := &MockTargeting{
			ID:   62,
			NegA: true,
			C:    []int{43, 56, 77, 64, 5, 34, 7, 57},
			D:    []int{8, 24, 87, 71, 12, 4, 55},
			NegD: true,
		}
		query := &Q{
			A: nil,
			B: nil,
			C: []int{88, 43},
			D: []int{12, 4, 6},
		}
		convey.So(d.Match(query.A, query.B, query.C, query.D), convey.ShouldBeFalse)
	})
}

//func TestBadCase(t *testing.T) {
//	b := NewIndexerBuilder()
//	doc := NewDocument(4)
//	doc.AddConjunction(NewConjunction().
//		NotIn("A", []int64{7, 96, 123, 137, 24, 124, 39, 114, 108, 9}).
//		NotIn("B", []int64{28, 66, 76, 12, 1, 109, 31, 146, 127, 37, 30, 133, 36, 112, 148, 111, 105, 139, 78}).
//		NotIn("C", []int64{108, 65, 68, 71, 103, 120, 6, 93, 123, 12, 52, 140, 105, 9, 17, 77, 78, 45, 81, 26, 66, 130, 34, 125, 80, 42}).
//		In("D", []int64{59, 57, 137, 45, 6, 123, 108, 56, 94, 90, 132, 130, 121, 99, 120, 122, 77, 27, 15, 103, 131, 52, 4, 133, 93, 48, 72, 98, 63, 24, 106, 144, 7, 74, 124, 46, 136, 62}))
//	err := b.AddDocument(doc)
//	fmt.Println("add doc:", err)
//
//	index := b.BuildIndex()
//	// PrintIndexInfo(index)
//	PrintIndexEntries(index)
//
//	// query: &{[32 66] [113 16] [122 1] [77 55]}
//	ids, err := index.Retrieve(map[core.BEField]Values{
//		"A": []int64{32, 66},
//		"B": []int64{113, 16},
//		"C": []int64{122, 1},
//		"D": []int64{77, 55},
//	}, WithDumpEntries(), WithStepDetail())
//	fmt.Println(ids, err)
//}

func TestSizeGroupedBEIndex_Retrieve(t *testing.T) {
	convey.Convey("test index negative logic", t, func() {
		docs, queries := BuildTestDocumentAndQueries(1000, 100, true)
		b := NewIndexerBuilder()
		for _, doc := range docs {
			err := b.AddDocument(doc.ToDocument())
			convey.So(err, convey.ShouldBeNil)
		}
		index, err := b.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		PrintIndexInfo(index)
		// PrintIndexEntries(index)

		idxRes := make(map[int]core.DocIDList)
		noneIdxRes := make(map[int]core.DocIDList)
		fmt.Println("queries count:", len(queries))
		start := time.Now().UnixNano() / 1000000
		for idx, q := range queries {
			var docIDS []core.DocID
			for id, target := range docs {
				if target.Match(q.A, q.B, q.C, q.D) {
					docIDS = append(docIDS, id)
				}
			}
			noneIdxRes[idx] = docIDS
		}
		fmt.Printf("NontIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

		start = time.Now().UnixNano() / 1000000
		for idx, ass := range queries {
			ids, err := index.Retrieve(ass.ToAssigns())
			convey.So(err, convey.ShouldBeNil)

			idxRes[idx] = ids
			if len(noneIdxRes[idx]) != len(ids) {
				ids, _ = index.Retrieve(ass.ToAssigns(), WithStepDetail())
				diff := ids.Sub(noneIdxRes[idx])
				diff = append(diff, noneIdxRes[idx].Sub(ids)...)
				sort.Sort(diff)
				for _, id := range diff {
					fmt.Println("doc:", docs[id])
				}
				fmt.Println("query:", ass)
				fmt.Println("idxRes:", ids)
				fmt.Println("noneIdxRes:", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		fmt.Printf("IndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
	})
}

func TestCompactedBEIndex_Retrieve(t *testing.T) {
	convey.Convey("test index negative logic", t, func() {
		docs, queries := BuildTestDocumentAndQueries(1000, 100, true)
		b := NewCompactIndexerBuilder()
		for _, doc := range docs {
			_ = b.AddDocument(doc.ToDocument())
		}
		compactedIndex, err := b.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		PrintIndexInfo(compactedIndex)

		idxUnionRes := make(map[int]core.DocIDList)
		noneIdxRes := make(map[int]core.DocIDList)
		fmt.Println("queries count:", len(queries))
		start := time.Now().UnixNano() / 1000000
		for idx, q := range queries {
			var docIDS []core.DocID
			for id, target := range docs {
				if target.Match(q.A, q.B, q.C, q.D) {
					docIDS = append(docIDS, id)
				}
			}
			noneIdxRes[idx] = docIDS
		}
		fmt.Printf("NontIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

		start = time.Now().UnixNano() / 1000000
		for idx, ass := range queries {
			ids, _ := compactedIndex.Retrieve(ass.ToAssigns())
			idxUnionRes[idx] = ids
			if len(ids) != len(noneIdxRes[idx]) {
				ids, _ = compactedIndex.Retrieve(ass.ToAssigns(), WithStepDetail())
				diff := ids.Sub(noneIdxRes[idx])
				diff = append(diff, noneIdxRes[idx].Sub(ids)...)
				sort.Sort(diff)
				for _, id := range diff {
					fmt.Println("doc:", docs[id])
				}
				sort.Sort(ids)
				sort.Sort(noneIdxRes[idx])
				fmt.Println("query:", util.JSONString(ass))
				fmt.Printf("IdxRes    :%+v\n", ids)
				fmt.Printf("NoneIdxRes:%+v\n", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		fmt.Printf("UnionIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
	})
}

func TestBEIndex_Retrieve2(t *testing.T) {
	LogLevel = ErrorLevel

	convey.Convey("test index and simple bench for kGroup/Compacted indexer", t, func() {
		docs, queries := BuildTestDocumentAndQueries(100000, 100, true)
		b := NewIndexerBuilder()
		cb := NewCompactIndexerBuilder()
		for _, doc := range docs {
			_ = b.AddDocument(doc.ToDocument())
			_ = cb.AddDocument(doc.ToDocument())
		}
		index, err := b.BuildIndex()
		util.PanicIfErr(err, "build index fail")
		compactedIndex, err := cb.BuildIndex()
		util.PanicIfErr(err, "build compact index fail")

		sb := &strings.Builder{}
		index.DumpIndexInfo(sb)
		fmt.Println("kGroupIndex summary", sb.String())

		sb.Reset()
		compactedIndex.DumpIndexInfo(sb)
		fmt.Println("compactedIndex summary", sb.String())

		queriesCnt := int64(len(queries))

		noneIdxRes := make(map[int]core.DocIDList)
		fmt.Println("queries count:", queriesCnt)
		start := time.Now()
		for idx, q := range queries {
			var docIDS []core.DocID
			for id, target := range docs {
				if target.Match(q.A, q.B, q.C, q.D) {
					docIDS = append(docIDS, id)
				}
			}
			noneIdxRes[idx] = docIDS
		}
		duration := time.Since(start)
		fmt.Printf("NoneIndexQuery Take %d(ms) %d(us)/ops\n",
			duration.Milliseconds(), duration.Microseconds()/queriesCnt)

		start = time.Now()
		idxRes := make(map[int]core.DocIDList)
		sum := 0
		for idx, ass := range queries {
			ids, _ := index.Retrieve(ass.ToAssigns())
			idxRes[idx] = ids
			sum += len(ids)
			if len(noneIdxRes[idx]) != len(ids) {
				sort.Sort(ids)
				sort.Sort(noneIdxRes[idx])

				for _, id := range ids {
					fmt.Println("doc:", docs[id])
				}
				fmt.Println("query:", ass)
				fmt.Println("idxRes:", ids)
				fmt.Println("noneIdxRes:", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		duration = time.Since(start)
		fmt.Println("avg result len:", int64(sum)/queriesCnt)
		fmt.Printf("KGroupIndexQuery Take %d(ms), %d(us)/ops\n",
			duration.Milliseconds(), duration.Microseconds()/queriesCnt)

		start = time.Now()
		idxUnionRes := make(map[int]core.DocIDList)
		for idx, ass := range queries {
			ids, _ := compactedIndex.Retrieve(ass.ToAssigns())
			idxUnionRes[idx] = ids
			if len(ids) != len(noneIdxRes[idx]) {
				sort.Sort(ids)
				sort.Sort(noneIdxRes[idx])
				for _, id := range ids {
					fmt.Println("doc:", docs[id])
				}
				fmt.Println("idx:", idx, ",query:", ass)
				fmt.Printf("unionIdxRes:%+v\n", ids)
				fmt.Printf("noneIdxRes:%+v\n", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		duration = time.Since(start)
		fmt.Printf("CompactedIndexQuery Take %d(ms), %d(us)/ops\n",
			duration.Milliseconds(), duration.Microseconds()/queriesCnt)
	})
}

/*
k: 2
K:2, res:[32], plgList:total plgs:2
0:
idx:0#72057594037927940#cur:<nil,nil> entries:[<10,true> <19,true> <27,true> <32,true> <54,true> <81,true>]
idx:1#72057594037927946#cur:<nil,nil> entries:[<3,true> <19,true> <35,true> <81,true>]
1:
idx:0#288230376151711758#cur:<nil,nil> entries:[<17,true> <32,true> <37,true>]
idx:1#288230376151711757#cur:<nil,nil> entries:[<19,true> <60,true>]
idx:2#288230376151711747#cur:<nil,nil> entries:[<53,true> <54,true>]
idx:3#288230376151711744#cur:<nil,nil> entries:[<17,true> <33,true>]
*/
func DocIDToIncludeEntries(ids []core.DocID, k int) (res []core.EntryID) {
	for _, id := range ids {
		res = append(res, core.NewEntryID(core.NewConjID(id, 0, k), true))
	}
	return res
}

func TestBEIndex_Retrieve3(t *testing.T) {
	plgs := FieldCursors{
		NewFieldCursor(EntriesCursors{
			NewSliceIterator(NewTerm("a", 1), DocIDToIncludeEntries([]core.DocID{17, 32, 37}, 2)),
			NewSliceIterator(NewTerm("a", 2), DocIDToIncludeEntries(core.DocIDList{17, 33}, 2)),
			NewSliceIterator(NewTerm("a", 3), DocIDToIncludeEntries(core.DocIDList{19, 60}, 2)),
			NewSliceIterator(NewTerm("a", 4), DocIDToIncludeEntries(core.DocIDList{53, 54}, 2)),
		}...),
		NewFieldCursor(EntriesCursors{
			NewSliceIterator(NewTerm("b", 1), DocIDToIncludeEntries(core.DocIDList{3, 19, 35, 81}, 2)),
			NewSliceIterator(NewTerm("b", 2), DocIDToIncludeEntries(core.DocIDList{10, 19, 27, 32, 54, 81}, 2)),
		}...),
	}

	ctx := newRetrieveCtx(nil)
	ctx.Collector = PickCollector()
	ctx.DumpStepInfo = true

	index := &KGroupsBEIndex{}
	convey.Convey("test retrieve k:2", t, func() {
		index.retrieveK(&ctx, plgs, 2)
		collector := ctx.Collector.(*DocIDCollector)
		convey.So(collector.GetDocIDs(), convey.ShouldResemble, core.DocIDList{19, 32, 54})
	})
}

func TestBEIndex_Retrieve4(t *testing.T) {
	LogLevel = ErrorLevel
	builder := NewIndexerBuilder()

	doc := core.NewDocument(12)
	doc.AddConjunction(core.NewConjunction().
		In("tag", 1).
		NotIn("age", core.NewInt32Values(40, 50, 60, 70)))

	convey.Convey("test doc add retrieve basic", t, func() {
		err := builder.AddDocument(doc)
		convey.So(err, convey.ShouldBeNil)

		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)

		for _, x := range []int32{40, 50, 60, 70} {
			result, e := indexer.Retrieve(core.Assignments{
				"age": x,
			})
			convey.So(e, convey.ShouldBeNil)
			convey.So(len(result), convey.ShouldEqual, 0)
		}
		result, e := indexer.Retrieve(core.Assignments{
			"age": core.NewInt32Values(40, 50, 60, 70),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 0)

		result, e = indexer.Retrieve(core.Assignments{
			"age": core.NewInt32Values(25),
			"tag": core.NewInt32Values(1),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(result, convey.ShouldResemble, core.DocIDList{12})

		result, e = indexer.Retrieve(core.Assignments{
			"age": core.NewIntValues(40), // age not in 40 so should be nil result
			"tag": core.NewInt32Values(1),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 0)

		convey.So(func() {
			result, e = indexer.Retrieve(core.Assignments{})

			result, e = indexer.Retrieve(core.Assignments{
				"age":             core.NewIntValues(40),
				"tag":             core.NewInt32Values(1),
				"not-found-field": core.NewInt32Values(1, 2, 3),
			})
		}, convey.ShouldNotPanic)

		customizedCollector := PickCollector()
		e = indexer.RetrieveWithCollector(core.Assignments{
			"age": core.NewInt32Values(25),
			"tag": core.NewInt32Values(1),
		}, customizedCollector)
		convey.So(e, convey.ShouldBeNil)
		convey.So(customizedCollector.GetDocIDs(), convey.ShouldResemble, core.DocIDList{12})
	})
}

func TestBEIndex_Retrieve5(t *testing.T) {
	LogLevel = DebugLevel
	builder := NewIndexerBuilder()

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := core.NewDocument(12)
	conj := core.NewConjunction().
		In("tag", core.NewInt32Values(1)).
		In("age", core.NewInt32Values(27, 50))
	conj2 := core.NewConjunction().
		In("tag", core.NewInt32Values(12))
	doc.AddConjunction(conj, conj2)
	_ = builder.AddDocument(doc)

	// 13: (tag IN 1 && age Not 27) or (tag Not 60)
	doc = core.NewDocument(13)
	conj = core.NewConjunction().
		In("tag", core.NewInt32Values(1)).
		NotIn("age", core.NewInt32Values(27))
	conj2 = core.NewConjunction().
		NotIn("age", core.NewInt32Values(60))
	doc.AddConjunction(conj, conj2)
	_ = builder.AddDocument(doc)

	// 14: (tag in 1,2 && tag in 12) or ("age In 60") or (sex In man)
	doc = core.NewDocument(14)
	conj = core.NewConjunction().
		In("tag", core.NewInt32Values(1, 2)).
		In("age", core.NewInt32Values(12))
	conj2 = core.NewConjunction().
		In("age", core.NewInt32Values(60))
	conj3 := core.NewConjunction().
		In("sex", core.NewStrValues("man"))
	doc.AddConjunction(conj, conj2, conj3)
	_ = builder.AddDocument(doc)

	convey.Convey("test SizeGroupedIndex Multi core.Conjunction retrieve", t, func() {
		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		PrintIndexInfo(indexer)
		var ids core.DocIDList
		ids, err = indexer.Retrieve(core.Assignments{
			"sex": []interface{}{"man"},
		}, WithStepDetail())
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{13, 14})
		convey.So(err, convey.ShouldBeNil)
		ids, err = indexer.Retrieve(core.Assignments{
			"sex": []interface{}{"female"},
			"age": []interface{}{60},
			"tag": []interface{}{61},
		})
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{14})
		convey.So(err, convey.ShouldBeNil)

		ids, err = indexer.Retrieve(core.Assignments{ //(tag not 60) + (tag in 1 && tag in 27)
			"sex": []interface{}{"female"},
			"age": []interface{}{27},
			"tag": []interface{}{1},
		})
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{12, 13})
		convey.So(err, convey.ShouldBeNil)
	})
}

func TestBEIndex_RetrievePartialConjunction(t *testing.T) {
	LogLevel = DebugLevel

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := core.NewDocument(12)
	doc.AddConjunction(core.NewConjunction().
		In("tag", core.NewInt32Values(1)).
		In("keyword", core.NewStrValues("abc", "棋牌")))
	doc.AddConjunction(core.NewConjunction().
		In("tag", core.NewInt32Values(1)).
		In("keyword", &cache.StrListValues{Values: []string{"abc"}})) // struct StrListValues can't be parsed

	convey.Convey("不允许一个doc部分conjunction异常", t, func() {
		builder := NewIndexerBuilder(WithBadConjBehavior(PanicBadConj))
		convey.So(func() {
			_ = builder.AddDocument(doc)
			_, _ = builder.BuildIndex()
		}, convey.ShouldPanic)
	})

	convey.Convey("conjunction异常时返回错误", t, func() {
		builder := NewIndexerBuilder(WithBadConjBehavior(ErrorBadConj))
		err := builder.AddDocument(doc)
		convey.So(err, convey.ShouldNotBeNil)
	})

	convey.Convey("部分Conjunction异常，不影响其他Conjunction匹配", t, func() {
		builder := NewIndexerBuilder(WithBadConjBehavior(SkipBadConj))
		_ = builder.AddDocument(doc)

		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		ids, err := indexer.Retrieve(core.Assignments{
			"tag":     []int{1, 2, 27},
			"keyword": core.NewStrValues("abc", "abc英文歌"),
		}, WithStepDetail())
		convey.So(err, convey.ShouldBeNil)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{12})
	})
}

func TestBEIndexer_Retrieve_Geohash(t *testing.T) {
	convey.Convey("geohash targeting", t, func() {
		RegisterFieldBuilder(HolderNameDefault, func() core.FieldIndexBuilder {
			holder := NewDefaultEntriesHolder()
			holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())
			holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))
			return holder
		})

		conj := core.NewConjunction().
			NotIn("tag", []int64{1, 2, 3, 4, 5}).
			In("geo", "31.21275902:121.53779984:1000")
		b := NewCompactIndexerBuilder()
		doc := core.NewDocument(1)
		doc.AddConjunction(conj)
		err := b.AddDocument(doc)
		convey.So(err, convey.ShouldBeNil)
		index, err := b.BuildIndex()
		convey.So(err, convey.ShouldBeNil)

		PrintIndexInfo(index)

		results, err := index.Retrieve(map[core.BEField]core.Values{
			"tag": 1000,
			"geo": [2]float64{31.21275902, 121.53779984},
		}, WithStepDetail())
		fmt.Println(results, err)
		convey.So(results.Len(), convey.ShouldEqual, 1)

		results, err = index.Retrieve(map[core.BEField]core.Values{
			"tag": 1000,
			"geo": [2]float64{30.21275902, 121.53779984},
		}, WithStepDetail())
		convey.So(results.Len(), convey.ShouldEqual, 0)
	})
}

func TestBEIndex_DumpLoad(t *testing.T) {
	convey.Convey("test dump and load", t, func() {
		// 1. Build Index
		builder := NewIndexerBuilder()
		doc := core.NewDocument(1)
		doc.AddConjunction(core.NewConjunction().
			In("tag", core.NewInt32Values(1)).
			In("age", core.NewInt32Values(27, 50)))
		_ = builder.AddDocument(doc)

		doc2 := core.NewDocument(2)
		doc2.AddConjunction(core.NewConjunction().
			In("tag", core.NewInt32Values(2)).
			NotIn("age", core.NewInt32Values(27)))
		_ = builder.AddDocument(doc2)

		index, err := builder.BuildIndex()
		util.PanicIfErr(err, "build index failed")

		// 2. Dump
		buf := &bytes.Buffer{}
		err = index.Dump(buf)
		convey.So(err, convey.ShouldBeNil)
		fmt.Printf("Dumped size: %d bytes\n", buf.Len())

		// 3. Load
		newIndex := NewKGroupsBEIndex()

		err = newIndex.Load(buf)
		convey.So(err, convey.ShouldBeNil)

		// 4. Retrieve from loaded index
		res, err := newIndex.Retrieve(core.Assignments{
			"tag": 1,
			"age": 27,
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(res, convey.ShouldResemble, core.DocIDList{1})

		res, err = newIndex.Retrieve(core.Assignments{
			"tag": 2,
			"age": 28, // Not 27
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(res, convey.ShouldResemble, core.DocIDList{2})
	})
}

func TestBEIndex_DumpLoad_TypeMismatch(t *testing.T) {
	convey.Convey("dump/load should reject indexer type mismatch", t, func() {
		// kgroups dump -> compact load should fail
		kgBuilder := NewIndexerBuilder()
		kgDoc := core.NewDocument(1)
		kgDoc.AddConjunction(core.NewConjunction().In("tag", core.NewInt32Values(1)).In("age", core.NewInt32Values(27)))
		_ = kgBuilder.AddDocument(kgDoc)
		kgIndex, _ := kgBuilder.BuildIndex()
		buf := &bytes.Buffer{}
		err := kgIndex.Dump(buf)
		convey.So(err, convey.ShouldBeNil)

		compact := NewCompactedBEIndex()
		err = compact.Load(buf)
		convey.So(err, convey.ShouldNotBeNil)

		// compact dump -> kgroups load should fail
		cBuilder := NewCompactIndexerBuilder()
		cDoc := core.NewDocument(2)
		cDoc.AddConjunction(core.NewConjunction().In("tag", core.NewInt32Values(2)).In("age", core.NewInt32Values(30)))
		_ = cBuilder.AddDocument(cDoc)
		cIndex, _ := cBuilder.BuildIndex()
		buf2 := &bytes.Buffer{}
		err = cIndex.Dump(buf2)
		convey.So(err, convey.ShouldBeNil)

		kg := NewKGroupsBEIndex()
		err = kg.Load(buf2)
		convey.So(err, convey.ShouldNotBeNil)
	})
}

func TestBEIndex_DumpLoad_ResetState(t *testing.T) {
	convey.Convey("load twice into same index instance should not mix old data", t, func() {
		// 1) First dump: conjunction size=2 (creates k=2 container)
		b1 := NewIndexerBuilder()
		d1 := core.NewDocument(1)
		d1.AddConjunction(core.NewConjunction().In("tag", core.NewInt32Values(1)).In("age", core.NewInt32Values(27)))
		_ = b1.AddDocument(d1)
		idx1, _ := b1.BuildIndex()
		buf1 := &bytes.Buffer{}
		err := idx1.Dump(buf1)
		convey.So(err, convey.ShouldBeNil)

		// 2) Second dump: conjunction size=1 (should drop k=2 container)
		b2 := NewIndexerBuilder()
		d2 := core.NewDocument(2)
		d2.AddConjunction(core.NewConjunction().In("tag", core.NewInt32Values(2)))
		_ = b2.AddDocument(d2)
		idx2, _ := b2.BuildIndex()
		buf2 := &bytes.Buffer{}
		err = idx2.Dump(buf2)
		convey.So(err, convey.ShouldBeNil)

		// 3) Load twice into the same receiver
		receiver := NewKGroupsBEIndex()
		err = receiver.Load(bytes.NewReader(buf1.Bytes()))
		convey.So(err, convey.ShouldBeNil)
		res, err := receiver.Retrieve(core.Assignments{"tag": 1, "age": 27})
		convey.So(err, convey.ShouldBeNil)
		convey.So(res, convey.ShouldResemble, core.DocIDList{1})

		err = receiver.Load(bytes.NewReader(buf2.Bytes()))
		convey.So(err, convey.ShouldBeNil)
		res, err = receiver.Retrieve(core.Assignments{"tag": 1, "age": 27})
		convey.So(err, convey.ShouldBeNil)
		convey.So(res.Len(), convey.ShouldEqual, 0)

		res, err = receiver.Retrieve(core.Assignments{"tag": 2})
		convey.So(err, convey.ShouldBeNil)
		convey.So(res, convey.ShouldResemble, core.DocIDList{2})
	})
}
