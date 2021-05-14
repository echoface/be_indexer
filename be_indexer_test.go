package be_indexer

import (
	"encoding/json"
	"fmt"
	"github.com/echoface/be_indexer/util"
	"io/ioutil"
	"math/rand"
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

func EntriesToDocs(entries Entries) (res []DocID) {
	for _, eid := range entries {
		res = append(res, eid.GetConjID().DocID())
	}
	return
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

	fmt.Println(indexer.DumpSizeEntries())

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
	ID DocID
	A  []int
	B  []int
	C  []int
	D  []int
}

func (t *MockTargeting) ToConj() *Conjunction {
	conj := NewConjunction()
	if len(t.A) > 0 {
		conj.In("A", NewIntValues(t.A...))
	}
	if len(t.B) > 0 {
		conj.In("B", NewIntValues(t.B...))
	}
	if len(t.C) > 0 {
		conj.In("C", NewIntValues(t.C...))
	}
	if len(t.D) > 0 {
		conj.In("D", NewIntValues(t.D...))
	}
	return conj
}

func valueMatch(values, queries []int) bool {
	if len(values) == 0 {
		return true
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

func (t *MockTargeting) Match(a, b, c, d []int) bool {
	if !valueMatch(t.A, a) {
		return false
	}
	if !valueMatch(t.B, b) {
		return false
	}
	if !valueMatch(t.C, c) {
		return false
	}
	if !valueMatch(t.D, d) {
		return false
	}
	return true
}

func randValue(cnt int) (res []int) {
	cnt = rand.Int() % cnt
	for i := 0; i < cnt; i++ {
		res = append(res, rand.Intn(2000))
	}
	return util.DistinctInt(res)
}

func TestBEIndex_Retrieve2(t *testing.T) {
	b := NewIndexerBuilder()
	targets := map[DocID]*MockTargeting{}

	LogLevel = ErrorLevel

	for i := 1; i < 100000; i++ {
		target := &MockTargeting{
			ID: DocID(i),
			A:  randValue(10),
			B:  randValue(50),
			C:  randValue(100),
			D:  randValue(150),
		}

		conj := target.ToConj()
		if len(conj.Expressions) > 0 {
			doc := NewDocument(target.ID)
			doc.AddConjunction(conj)
			b.AddDocument(doc)

			targets[DocID(i)] = target
		}
	}

	index := b.BuildIndex()

	type Q struct {
		A []int
		B []int
		C []int
		D []int
	}

	var Qs []Q
	var assigns []Assignments

	for i := 0; i < 1; i++ {
		q := Q{
			A: randValue(1),
			B: randValue(2),
			C: randValue(2),
			D: randValue(1),
		}
		Qs = append(Qs, q)
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
		assigns = append(assigns, assign)
	}

	idxRes := make(map[int][]DocID)
	idxUnionRes := make(map[int][]DocID)
	noneIdxRes := make(map[int][]DocID)

	start := time.Now().UnixNano() / 1000000
	for idx, q := range Qs {
		for id, target := range targets {
			if target.Match(q.A, q.B, q.C, q.D) {
				noneIdxRes[idx] = append(noneIdxRes[idx], id)
			}
		}
	}
	fmt.Printf("NontIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

	start = time.Now().UnixNano() / 1000000
	for idx, ass := range assigns {
		ids, _ := index.Retrieve(ass)
		idxRes[idx] = ids
		if len(noneIdxRes[idx]) != len(ids) {
			fmt.Println("idxRes:", ids)
			fmt.Println("noneIdxRes:", noneIdxRes[idx])
			fmt.Println(index.DumpSizeEntries())

			panic(nil)
		}
	}
	fmt.Printf("IndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

	start = time.Now().UnixNano() / 1000000
	for idx, ass := range assigns {
		ids, _ := index.UnionRetrieve(ass)
		idxUnionRes[idx] = ids
		if len(ids) != len(noneIdxRes[idx]) {
			fmt.Printf("unionIdxRes:%+v\n", ids)
			fmt.Printf("noneIdxRes:%+v\n", noneIdxRes[idx])
			fmt.Println(index.DumpUnionEntries())
			panic(nil)
		}
	}
	fmt.Printf("UnionIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
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
	plgs := FieldPostingListGroups{
		NewFieldPostingListGroup(PostingLists{
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
		NewFieldPostingListGroup(PostingLists{
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

	index := &BEIndex{}
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
	fmt.Println(indexer.UnionRetrieve(Assignments{
		"age": NewInt32Values2(1),
	}))

	fmt.Println(indexer.Retrieve(Assignments{
		"age": NewInt32Values2(25),
		"tag": NewInt32Values2(1),
	}))
	fmt.Println(indexer.UnionRetrieve(Assignments{
		"age": NewInt32Values2(25),
		"tag": NewInt32Values2(1),
	}))

	fmt.Println(indexer.Retrieve(Assignments{
		"age": NewIntValues2(40),
		"tag": NewInt32Values2(1),
	}))
	fmt.Println(indexer.UnionRetrieve(Assignments{
		"age": NewIntValues2(40),
		"tag": NewInt32Values2(1),
	}))
}
