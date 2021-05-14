package main

import (
	"encoding/json"
	"fmt"
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
	"math/rand"
	"time"
)

type MockTargeting struct {
	ID be_indexer.DocID
	A  []int
	B  []int
	C  []int
	D  []int
}

func (t *MockTargeting) ToConj() *be_indexer.Conjunction {
	conj := be_indexer.NewConjunction()
	if len(t.A) > 0 {
		conj.In("A", be_indexer.NewIntValues(t.A...))
	}
	if len(t.B) > 0 {
		conj.In("B", be_indexer.NewIntValues(t.B...))
	}
	if len(t.C) > 0 {
		conj.In("C", be_indexer.NewIntValues(t.C...))
	}
	if len(t.D) > 0 {
		conj.In("D", be_indexer.NewIntValues(t.D...))
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

func main() {
	b := be_indexer.NewIndexerBuilder()
	targets := map[be_indexer.DocID]*MockTargeting{}

	be_indexer.LogLevel = be_indexer.ErrorLevel

	for i := 1; i < 100000; i++ {
		target := &MockTargeting{
			ID: be_indexer.DocID(i),
			A:  randValue(10),
			B:  randValue(50),
			C:  randValue(100),
			D:  randValue(150),
		}

		conj := target.ToConj()
		if len(conj.Expressions) > 0 {
			doc := be_indexer.NewDocument(target.ID)
			doc.AddConjunction(conj)
			b.AddDocument(doc)

			targets[be_indexer.DocID(i)] = target
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
	var assigns []be_indexer.Assignments

	for i := 0; i < 10; i++ {
		q := Q{
			A: randValue(1),
			B: randValue(2),
			C: randValue(2),
			D: randValue(1),
		}
		Qs = append(Qs, q)
		assign := be_indexer.Assignments{}
		if len(q.A) > 0 {
			assign["A"] = be_indexer.NewIntValues(q.A...)
		}
		if len(q.B) > 0 {
			assign["B"] = be_indexer.NewIntValues(q.B...)
		}
		if len(q.C) > 0 {
			assign["C"] = be_indexer.NewIntValues(q.C...)
		}
		if len(q.D) > 0 {
			assign["D"] = be_indexer.NewIntValues(q.D...)
		}
		assigns = append(assigns, assign)
	}

	idxRes := make(map[int][]be_indexer.DocID)
	idxUnionRes := make(map[int][]be_indexer.DocID)
	noneIdxRes := make(map[int][]be_indexer.DocID)

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
