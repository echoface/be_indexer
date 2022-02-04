package be_indexer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"github.com/smartystreets/goconvey/convey"
)

func buildTestDoc() []*Document {

	docs := make([]*Document, 0)
	content, e := ioutil.ReadFile("./test_data/test_docs.json")
	util.PanicIfErr(e, "load test docs fail")

	if e = json.Unmarshal(content, &docs); e != nil {
		panic(e)
	}
	fmt.Println("total docs:", len(docs))
	return docs
}

func TestBEIndex_Retrieve(t *testing.T) {
	LogLevel = InfoLevel

	var err error
	builder := NewIndexerBuilder()

	for _, doc := range buildTestDoc() {
		err = builder.AddDocument(doc)
		util.PanicIfErr(err, "add document fail")
	}

	indexer := builder.BuildIndex()

	fmt.Println(indexer.DumpEntries())

	result, e := indexer.Retrieve(map[BEField]Values{
		"age": NewValues2(5),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[BEField]Values{
		"ip": NewStrValues2("localhost"),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[BEField]Values{
		"age":  NewIntValues2(1),
		"city": NewStrValues2("sh"),
		"tag":  NewValues2("tag1"),
	})
	fmt.Println(e, result)
}

type MockTargeting struct {
	ID   DocID
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

func (q *Q) ToAssigns() Assignments {
	assign := Assignments{}
	if len(q.A) > 0 {
		assign["A"] = NewIntValues(q.A...)
	}
	if len(q.B) > 0 {
		assign["B"] = NewIntValues(q.B...)
	}
	if len(q.C) > 0 {
		assign["C"] = NewIntValues(q.C...)
	}
	if len(q.D) > 0 {
		assign["D"] = NewIntValues(q.D...)
	}
	return assign
}

func (t *MockTargeting) ToConj() *Conjunction {
	conj := NewConjunction()
	if len(t.A) > 0 {
		if t.NegA {
			conj.NotIn("A", NewIntValues(t.A...))
		} else {
			conj.In("A", NewIntValues(t.A...))
		}
	}
	if len(t.B) > 0 {
		if t.NegB {
			conj.NotIn("B", NewIntValues(t.B...))
		} else {
			conj.In("B", NewIntValues(t.B...))
		}
	}
	if len(t.C) > 0 {
		if t.NegC {
			conj.NotIn("C", NewIntValues(t.C...))
		} else {
			conj.In("C", NewIntValues(t.C...))
		}
	}
	if len(t.D) > 0 {
		if t.NegD {
			conj.NotIn("D", NewIntValues(t.D...))
		} else {
			conj.In("D", NewIntValues(t.D...))
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

func (t *MockTargeting) ToDocument() *Document {
	conj := t.ToConj()
	if len(conj.Expressions) == 0 {
		return nil
	}
	doc := NewDocument(t.ID)
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

func BuildTestDocumentAndQueries(docCnt, queriesCnt int, withNeg bool) (map[DocID]*MockTargeting, []*Q) {
	docs := make(map[DocID]*MockTargeting)
	for len(docs) < docCnt {
		target := &MockTargeting{
			ID: DocID(len(docs) + 1),
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
			A: randValue(8),
			B: randValue(6),
			C: randValue(4),
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

func TestSizeGroupedBEIndex_Retrieve(t *testing.T) {
	convey.Convey("test index negative logic", t, func() {
		docs, queries := BuildTestDocumentAndQueries(100000, 1000, true)
		b := NewIndexerBuilder()
		for _, doc := range docs {
			b.AddDocument(doc.ToDocument())
		}
		index := b.BuildIndex()
		fmt.Println("summary", index.DumpEntriesSummary())

		idxRes := make(map[int]DocIDList)
		noneIdxRes := make(map[int]DocIDList)
		fmt.Println("queries count:", len(queries))
		start := time.Now().UnixNano() / 1000000
		for idx, q := range queries {
			var docIDS []DocID
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
			ids, _ := index.Retrieve(ass.ToAssigns())
			idxRes[idx] = ids
			if len(noneIdxRes[idx]) != len(ids) {
				diff := ids.Sub(noneIdxRes[idx])
				diff = append(diff, noneIdxRes[idx].Sub(ids)...)
				sort.Sort(diff)
				//fmt.Println(index.DumpEntries())
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
		docs, queries := BuildTestDocumentAndQueries(10000, 1000, true)
		b := NewIndexerBuilder()
		for _, doc := range docs {
			b.AddDocument(doc.ToDocument())
		}
		compactedIndex := b.BuildIndex()
		fmt.Println("compactedIndex summary", compactedIndex.DumpEntriesSummary())

		idxUnionRes := make(map[int]DocIDList)
		noneIdxRes := make(map[int]DocIDList)
		fmt.Println("queries count:", len(queries))
		start := time.Now().UnixNano() / 1000000
		for idx, q := range queries {
			var docIDS []DocID
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
				diff := ids.Sub(noneIdxRes[idx])
				diff = append(diff, noneIdxRes[idx].Sub(ids)...)
				sort.Sort(diff)
				for _, id := range diff {
					fmt.Println("doc:", docs[id])
				}
				sort.Sort(ids)
				sort.Sort(noneIdxRes[idx])
				fmt.Println("query:", ass)
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
		docs, queries := BuildTestDocumentAndQueries(10000, 10000, false)
		b := NewIndexerBuilder()
		cb := NewCompactIndexerBuilder()
		for _, doc := range docs {
			b.AddDocument(doc.ToDocument())
			cb.AddDocument(doc.ToDocument())
		}
		index := b.BuildIndex()
		compactedIndex := cb.BuildIndex()

		fmt.Println("kGroupIndex summary", index.DumpEntriesSummary())
		fmt.Println("compactedIndex summary", compactedIndex.DumpEntriesSummary())

		queriesCnt := int64(len(queries))

		noneIdxRes := make(map[int]DocIDList)
		fmt.Println("queries count:", queriesCnt)
		start := time.Now()
		for idx, q := range queries {
			var docIDS []DocID
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
		idxRes := make(map[int]DocIDList)
		for idx, ass := range queries {
			ids, _ := index.Retrieve(ass.ToAssigns())
			idxRes[idx] = ids
			if len(noneIdxRes[idx]) != len(ids) {
				sort.Sort(ids)
				sort.Sort(noneIdxRes[idx])
				fmt.Println(index.DumpEntries())
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
		fmt.Printf("KGroupIndexQuery Take %d(ms), %d(us)/ops\n",
			duration.Milliseconds(), duration.Microseconds()/queriesCnt)

		start = time.Now()
		idxUnionRes := make(map[int]DocIDList)
		for idx, ass := range queries {
			ids, _ := compactedIndex.Retrieve(ass.ToAssigns())
			idxUnionRes[idx] = ids
			if len(ids) != len(noneIdxRes[idx]) {
				fmt.Println(index.DumpEntries())
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
func DocIDToIncludeEntries(ids []DocID, k int) (res []EntryID) {
	for _, id := range ids {
		res = append(res, NewEntryID(NewConjID(id, 0, k), true))
	}
	return res
}

func TestBEIndex_Retrieve3(t *testing.T) {
	plgs := FieldCursors{
		NewFieldCursor(CursorGroup{
			{
				entries: DocIDToIncludeEntries([]DocID{17, 32, 37}, 2),
			},
			{
				entries: DocIDToIncludeEntries(DocIDList{17, 33}, 2),
			},
			{
				entries: DocIDToIncludeEntries(DocIDList{19, 60}, 2),
			},
			{
				entries: DocIDToIncludeEntries(DocIDList{53, 54}, 2),
			},
		}...),
		NewFieldCursor(CursorGroup{
			{
				entries: DocIDToIncludeEntries(DocIDList{10, 19, 27, 32, 54, 81}, 2),
			},
			{
				entries: DocIDToIncludeEntries(DocIDList{3, 19, 35, 81}, 2),
			},
		}...),
	}
	for _, plg := range plgs {
		plg.current = plg.cursorGroup[0]
	}

	ctx := newRetrieveCtx(nil)
	ctx.collector = PickCollector()

	index := &SizeGroupedBEIndex{}
	convey.Convey("test retrieve k:2", t, func() {
		index.retrieveK(&ctx, plgs, 2)
		collector := ctx.collector.(*DocIDCollector)
		convey.So(collector.GetDocIDs(), convey.ShouldResemble, DocIDList{19, 32, 54})
	})
}

func TestBEIndex_Retrieve4(t *testing.T) {
	LogLevel = ErrorLevel
	builder := NewIndexerBuilder()

	doc := NewDocument(12)
	doc.AddConjunction(NewConjunction().
		In("tag", NewInt32Values2(1)).
		NotIn("age", NewInt32Values2(40, 50, 60, 70)))

	convey.Convey("test doc add retrieve basic", t, func() {
		err := builder.AddDocument(doc)
		convey.So(err, convey.ShouldBeNil)

		indexer := builder.BuildIndex()

		for _, x := range []int32{40, 50, 60, 70} {
			result, e := indexer.Retrieve(Assignments{
				"age": NewInt32Values2(x),
			})
			convey.So(e, convey.ShouldBeNil)
			convey.So(len(result), convey.ShouldEqual, 0)
		}
		result, e := indexer.Retrieve(Assignments{
			"age": NewInt32Values2(40, 50, 60, 70),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 0)

		result, e = indexer.Retrieve(Assignments{
			"age": NewInt32Values2(25),
			"tag": NewInt32Values2(1),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(result, convey.ShouldResemble, DocIDList{12})

		result, e = indexer.Retrieve(Assignments{
			"age": NewIntValues2(40), // age not in 40 so should be nil result
			"tag": NewInt32Values2(1),
		})
		convey.So(e, convey.ShouldBeNil)
		convey.So(len(result), convey.ShouldEqual, 0)

		convey.So(func() {
			result, e = indexer.Retrieve(Assignments{})

			result, e = indexer.Retrieve(Assignments{
				"age":             NewIntValues2(40),
				"tag":             NewInt32Values2(1),
				"not-found-field": NewInt32Values2(1, 2, 3),
			})

		}, convey.ShouldNotPanic)

		customizedCollector := PickCollector()
		e = indexer.RetrieveWithCollector(Assignments{
			"age": NewInt32Values2(25),
			"tag": NewInt32Values2(1),
		}, customizedCollector)
		convey.So(e, convey.ShouldBeNil)
		convey.So(customizedCollector.GetDocIDs(), convey.ShouldResemble, DocIDList{12})
	})
}

func TestBEIndex_Retrieve5(t *testing.T) {
	LogLevel = DebugLevel
	builder := NewIndexerBuilder()

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := NewDocument(12)
	conj := NewConjunction().
		In("tag", NewInt32Values2(1)).
		In("age", NewInt32Values2(27, 50))
	conj2 := NewConjunction().
		In("tag", NewInt32Values2(12))
	doc.AddConjunction(conj, conj2)
	builder.AddDocument(doc)

	// 13: (tag IN 1 && age Not 27) or (tag Not 60)
	doc = NewDocument(13)
	conj = NewConjunction().
		In("tag", NewInt32Values2(1)).
		NotIn("age", NewInt32Values2(27))
	conj2 = NewConjunction().
		NotIn("age", NewInt32Values2(60))
	doc.AddConjunction(conj, conj2)
	builder.AddDocument(doc)

	// 14: (tag in 1,2 && tag in 12) or ("age In 60") or (sex In man)
	doc = NewDocument(14)
	conj = NewConjunction().
		In("tag", NewInt32Values2(1, 2)).
		In("age", NewInt32Values2(12))
	conj2 = NewConjunction().
		In("age", NewInt32Values2(60))
	conj3 := NewConjunction().
		In("sex", NewStrValues("man"))
	doc.AddConjunction(conj, conj2, conj3)
	builder.AddDocument(doc)

	convey.Convey("test SizeGroupedIndex Multi Conjunction retrieve", t, func() {

		indexer := builder.BuildIndex()
		fmt.Println(indexer.DumpEntries())

		var err error
		var ids DocIDList
		ids, err = indexer.Retrieve(Assignments{
			"sex": []interface{}{"man"},
		}, WithDumpEntries(), WithStepDetail())
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, DocIDList{13, 14})
		convey.So(err, convey.ShouldBeNil)
		ids, err = indexer.Retrieve(Assignments{
			"sex": []interface{}{"female"},
			"age": []interface{}{60},
			"tag": []interface{}{61},
		})
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, DocIDList{14})
		convey.So(err, convey.ShouldBeNil)

		ids, err = indexer.Retrieve(Assignments{ //(tag not 60) + (tag in 1 && tag in 27)
			"sex": []interface{}{"female"},
			"age": []interface{}{27},
			"tag": []interface{}{1},
		})
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, DocIDList{12, 13})
		convey.So(err, convey.ShouldBeNil)
	})
}

func TestBEIndex_Retrieve6(t *testing.T) {
	LogLevel = ErrorLevel
	builder := NewIndexerBuilder()
	builder.ConfigField("keyword", FieldOption{
		Parser:    parser.ParserNameCommon,
		Container: HolderNameACMatcher,
	})

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := NewDocument(12)
	conj := NewConjunction().
		In("tag", NewInt32Values2(1)).
		In("keyword", NewStrValues("abc", "红包", "棋牌"))
	doc.AddConjunction(conj)
	builder.AddDocument(doc)

	// 13: (tag IN 1 && age Not 27) or (tag Not 60)
	doc = NewDocument(13)
	conj = NewConjunction().
		In("tag", NewInt32Values2(1)).NotIn("age", NewInt32Values2(27, 15, 18, 22, 28, 32))
	doc.AddConjunction(conj)
	builder.AddDocument(doc)

	// 14: (tag in 1,2 && tag in 12) or ("age In 60") or (sex In man)
	doc = NewDocument(14)
	conj = NewConjunction().
		In("tag", NewInt32Values2(1, 2)).
		In("sex", NewStrValues("women"))
	conj3 := NewConjunction().
		NotIn("keyword", NewStrValues("红包", "拉拉", "解放")).
		In("age", NewIntValues(12, 24, 28))
	doc.AddConjunction(conj, conj3)
	builder.AddDocument(doc)

	convey.Convey("test ac matcher retrieve", t, func() {

		indexer := builder.BuildIndex()

		var err error
		var ids DocIDList
		ids, err = indexer.Retrieve(Assignments{
			"sex":     []interface{}{"man"},
			"keyword": NewStrValues(string("解放军发红包"), "abc英文歌"),
			"age":     []interface{}{28, 2, 27},
			"tag":     []interface{}{1, 2, 27},
		})
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, DocIDList{12})
		convey.So(err, convey.ShouldBeNil)
	})
}
