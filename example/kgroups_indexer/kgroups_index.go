package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
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

func BuildIndex() be_indexer.BEIndex {
	b := be_indexer.NewIndexerBuilder()
	targets := map[be_indexer.DocID]*MockTargeting{}
	for i := 1; i < docCount; i++ {
		target := &MockTargeting{
			ID: be_indexer.DocID(i),
			A:  randValue(3),
			B:  randValue(5),
			C:  randValue(10),
			D:  randValue(30),
		}

		conj := target.ToConj()
		if len(conj.Expressions) > 0 {
			doc := be_indexer.NewDocument(target.ID)
			doc.AddConjunction(conj)
			b.AddDocument(doc)

			targets[be_indexer.DocID(i)] = target
		}
	}
	return b.BuildIndex()
}

func QueryTest(index be_indexer.BEIndex) {
	type Q struct {
		A []int
		B []int
		C []int
		D []int
	}

	var Qs []Q
	var assigns []be_indexer.Assignments

	for i := 0; i < 1000; i++ {
		q := Q{
			A: randValue(10),
			B: randValue(5),
			C: randValue(3),
			D: randValue(2),
		}
		Qs = append(Qs, q)
		assign := be_indexer.Assignments{}
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

	start := time.Now().UnixNano() / 1000000
	cnt := 0
	for _, ass := range assigns {
		ids, _ := index.Retrieve(ass)
		cnt += len(ids)
	}
	fmt.Println("avg result len:", float64(cnt)/float64(len(assigns)))
	fmt.Printf("SizeGroupedIndexQuery Take %d(ms)\n", time.Now().UnixNano()/1000000-start)

}

func main() {
	flag.Parse()

	go func() {
		_ = http.ListenAndServe("localhost:6061", nil)
	}()

	be_indexer.LogLevel = be_indexer.ErrorLevel

	index := BuildIndex()
	sb := &strings.Builder{}
	index.DumpIndexInfo(sb)
	fmt.Println("size grouped summary:", sb.String())

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
	QueryTest(index)

	runtime.GC()

	if enableProfiling {
		time.Sleep(time.Minute * 10)
	}
}
