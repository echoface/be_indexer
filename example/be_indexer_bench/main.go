package main

import (
	"github.com/echoface/be_indexer/core"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
)

type MockTargeting struct {
	ID core.DocID
	A  []int
	B  []int
	C  []int
	D  []int
}

func (t *MockTargeting) ToConj() *core.Conjunction {
	conj := core.NewConjunction()
	if len(t.A) > 0 {
		conj.In("A", t.A)
	}
	if len(t.B) > 0 {
		conj.In("B", t.B)
	}
	if len(t.C) > 0 {
		conj.In("C", t.C)
	}
	if len(t.D) > 0 {
		conj.In("D", t.D)
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
		res = append(res, rand.Intn(200))
	}
	return util.DistinctInt(res)
}

var docCount int
var enableProfiling bool

func init() {
	flag.IntVar(&docCount, "c", 100000, "index document count")
	flag.BoolVar(&enableProfiling, "profile", false, "enable cpu profiling")
}

func main() {
	flag.Parse()

	b := be_indexer.NewIndexerBuilder()
	cb := be_indexer.NewCompactIndexerBuilder()

	targets := map[core.DocID]*MockTargeting{}

	be_indexer.LogLevel = be_indexer.ErrorLevel

	for i := 1; i < docCount; i++ {
		target := &MockTargeting{
			ID: core.DocID(i),
			A:  randValue(3),
			B:  randValue(5),
			C:  randValue(10),
			D:  randValue(30),
		}

		conj := target.ToConj()
		if len(conj.Predicates) > 0 {
			doc := core.NewDocument(target.ID)
			doc.AddConjunction(conj)

			util.PanicIfErr(b.AddDocument(doc), "build doc fail, doc:%s", doc.String())
			util.PanicIfErr(cb.AddDocument(doc), "build doc fail, doc:%s", doc.String())

			targets[core.DocID(i)] = target
		}
	}

	index, err := b.BuildIndex()
	util.PanicIfErr(err, "build index fail")
	sb := &strings.Builder{}
	index.DumpIndexInfo(sb)
	fmt.Println("index summary:", sb.String())

	compactedIndex, err := cb.BuildIndex()
	util.PanicIfErr(err, "build compacted index fail")
	sb.Reset()
	compactedIndex.DumpIndexInfo(sb)
	fmt.Println("compactedIndex summary:", sb.String())

	type Q struct {
		A []int
		B []int
		C []int
		D []int
	}

	var Qs []Q
	var assigns []core.Assignments

	for i := 0; i < 1000; i++ {
		q := Q{
			A: randValue(10),
			B: randValue(5),
			C: randValue(3),
			D: randValue(2),
		}
		Qs = append(Qs, q)
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
		assigns = append(assigns, assign)
	}

	idxRes := make(map[int][]core.DocID)
	idxUnionRes := make(map[int][]core.DocID)
	//noneIdxRes := make(map[int][]core.DocID)

	if enableProfiling {
		f, err := os.OpenFile("cpu.prof", os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			panic(err)
		}
		pprof.StartCPUProfile(f)
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
			time.Sleep(time.Second)
		}()
	}
	start := time.Now().UnixNano() / 1000000
	//for idx, q := range Qs {
	//	for id, target := range targets {
	//		if target.Match(q.A, q.B, q.C, q.D) {
	//			noneIdxRes[idx] = append(noneIdxRes[idx], id)
	//		}
	//	}
	//}
	fmt.Printf("NontIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

	start = time.Now().UnixNano() / 1000000
	for idx, ass := range assigns {
		ids, _ := index.Retrieve(ass)
		idxRes[idx] = ids
		//if len(noneIdxRes[idx]) != len(ids) {
		//	fmt.Println("idxRes:", ids)
		//	fmt.Println("noneIdxRes:", noneIdxRes[idx])
		//	fmt.Println(index.DumpSizeEntries())
		//
		//	panic(nil)
		//}
	}
	fmt.Printf("SizeGroupedIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

	start = time.Now().UnixNano() / 1000000
	for idx, ass := range assigns {
		ids, _ := compactedIndex.Retrieve(ass)
		idxUnionRes[idx] = ids
		//if len(ids) != len(noneIdxRes[idx]) {
		//	fmt.Printf("unionIdxRes:%+v\n", ids)
		//	fmt.Printf("noneIdxRes:%+v\n", noneIdxRes[idx])
		//	fmt.Println(index.DumpUnionEntries())
		//	panic(nil)
		//}
	}
	fmt.Printf("CompactedIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)
}
