package be_indexer

import (
	"encoding/json"
	"fmt"
	"github.com/echoface/be_indexer/util"
	"github.com/smartystreets/goconvey/convey"
	"io/ioutil"
	"math/rand"
	"sort"
	"testing"
	"time"
)

func buildTestDoc() []*Document {

	docs := make([]*Document, 0)
	content, e := ioutil.ReadFile("./test_data/test_docs.json")
	if e != nil {
		panic(e)
	}
	if e = json.Unmarshal(content, &docs); e != nil {
		panic(e)
	}
	fmt.Println("total docs:", len(docs))
	return docs
}

func TestBEIndex_Retrieve(t *testing.T) {
	LogLevel = InfoLevel

	builder := IndexerBuilder{
		Documents: make(map[DocID]*Document),
	}

	for _, doc := range buildTestDoc() {
		builder.AddDocument(doc)
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
			conj.NotIn("C", NewIntValues(t.B...))
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

	//if !containAny(t.A, a) {
	//	return false
	//}
	//if !containAny(t.B, b) {
	//	return false
	//}
	//if !containAny(t.C, c) {
	//	return false
	//}
	//if !containAny(t.D, d) {
	//	return false
	//}
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

func TestCompactedBEIndex_Retrieve(t *testing.T) {
	convey.Convey("test index negative logic", t, func() {
		docs, queries := BuildTestDocumentAndQueries(100, 10000, true)
		b := NewIndexerBuilder()
		for _, doc := range docs {
			b.AddDocument(doc.ToDocument())
		}
		index := b.BuildIndex()
		compactedIndex := b.BuildCompactedIndex()
		fmt.Println("summary", index.DumpEntriesSummary())
		fmt.Println("compactedIndex summary", compactedIndex.DumpEntriesSummary())

		idxRes := make(map[int]DocIDList)
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
		fmt.Printf("IndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

		start = time.Now().UnixNano() / 1000000
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
				fmt.Println("query:", ass)
				fmt.Printf("unionIdxRes:%+v\n", ids)
				fmt.Printf("noneIdxRes:%+v\n", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		fmt.Printf("UnionIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
	})
}

func TestBEIndex_Retrieve2(t *testing.T) {
	LogLevel = ErrorLevel

	convey.Convey("test index logic", t, func() {
		docs, queries := BuildTestDocumentAndQueries(10000, 10000, false)
		b := NewIndexerBuilder()
		for _, doc := range docs {
			b.AddDocument(doc.ToDocument())
		}
		index := b.BuildIndex()
		compactedIndex := b.BuildCompactedIndex()
		fmt.Println("summary", index.DumpEntriesSummary())
		fmt.Println("compactedIndex summary", compactedIndex.DumpEntriesSummary())

		idxRes := make(map[int]DocIDList)
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
		fmt.Printf("IndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

		start = time.Now().UnixNano() / 1000000
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
				fmt.Println("query:", ass)
				fmt.Printf("unionIdxRes:%+v\n", ids)
				fmt.Printf("noneIdxRes:%+v\n", noneIdxRes[idx])
				convey.So(nil, convey.ShouldNotBeNil)
			}
		}
		fmt.Printf("UnionIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
	})

}

/*
gonghuan, k: 2
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
	plgs := FieldScannerGroups{
		NewFieldPostingListGroup(EntriesScanners{
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
		NewFieldPostingListGroup(EntriesScanners{
			{
				entries: DocIDToIncludeEntries(DocIDList{10, 19, 27, 32, 54, 81}, 2),
			},
			{
				entries: DocIDToIncludeEntries(DocIDList{3, 19, 35, 81}, 2),
			},
		}...),
	}
	for _, plg := range plgs {
		plg.current = plg.plGroup[0]
	}

	index := &SizeGroupedBEIndex{}
	fmt.Println(index.retrieveK(plgs, 2))
}

func TestBEIndex_Retrieve4(t *testing.T) {
	LogLevel = ErrorLevel
	builder := NewIndexerBuilder()

	doc := NewDocument(12)
	conj := NewConjunction()
	conj.In("tag", NewInt32Values2(1))
	conj.NotIn("age", NewInt32Values2(40, 50, 60, 70))

	doc.AddConjunction(conj)

	builder.AddDocument(doc)

	indexer := builder.BuildIndex()

	fmt.Println(indexer.Retrieve(Assignments{
		"age": NewInt32Values2(1),
	}))
	fmt.Println(indexer.Retrieve(Assignments{
		"age": NewInt32Values2(25),
		"tag": NewInt32Values2(1),
	}))

	fmt.Println(indexer.Retrieve(Assignments{
		"age": NewIntValues2(40),
		"tag": NewInt32Values2(1),
	}))
}
