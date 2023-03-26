package main

import (
	"fmt"

	"github.com/echoface/be_indexer/parser"

	idx "github.com/echoface/be_indexer"
	rridx "github.com/echoface/be_indexer/roaringidx"
	"github.com/echoface/be_indexer/util"
)

type CaseData struct {
	q      idx.Assignments
	expect []uint64
}

func genDocs() (docs []*idx.Document, caseData []CaseData) {
	docs = make([]*idx.Document, 0)

	doc := idx.NewDocument(1).AddConjunction(
		idx.NewConjunction().
			In("age", []int{20, 30, 40}).
			NotIn("age", []int{30, 50}).
			In("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	doc = idx.NewDocument(2).AddConjunction(
		idx.NewConjunction().
			In("age", []int{20, 30, 40}).
			NotIn("age", []int{30, 50}).
			NotIn("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	doc = idx.NewDocument(3).AddConjunction(
		idx.NewConjunction().
			In("age", []int{20, 30, 40}).
			In("age", []int{30, 50}).
			In("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	doc = idx.NewDocument(4).AddConjunction(
		idx.NewConjunction().
			In("age", []int{20, 30, 40}).
			In("age", []int{30, 50}).
			NotIn("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	doc = idx.NewDocument(5).AddConjunction(
		idx.NewConjunction().
			NotIn("age", []int{20, 30, 40}).
			NotIn("age", []int{30, 50}).
			In("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	doc = idx.NewDocument(6).AddConjunction(
		idx.NewConjunction().
			NotIn("age", []int{20, 30, 40}).
			NotIn("age", []int{30, 50}).
			NotIn("city", idx.NewStrValues("bj", "sh")))
	docs = append(docs, doc)

	caseData = []CaseData{
		{q: map[idx.BEField]idx.Values{"age": 30, "city": "hn"}, expect: []uint64{4}},
		{q: map[idx.BEField]idx.Values{"age": 30, "city": "bj"}, expect: []uint64{3}},
		{q: map[idx.BEField]idx.Values{"age": 40, "city": "hn"}, expect: []uint64{2, 4}},
		{q: map[idx.BEField]idx.Values{"age": 40, "city": "bj"}, expect: []uint64{1, 3}},
		{q: map[idx.BEField]idx.Values{"city": "hn"}, expect: []uint64{6}},
		{q: map[idx.BEField]idx.Values{"city": "bj"}, expect: []uint64{5}},
		{q: map[idx.BEField]idx.Values{"age": 30}, expect: []uint64{4}},
		{q: map[idx.BEField]idx.Values{"age": 40}, expect: []uint64{2, 4}},
		{q: map[idx.BEField]idx.Values{}, expect: []uint64{6}},
	}
	return docs, caseData
}

func RunRRIndexing() {
	b := rridx.NewIndexerBuilder()
	_ = b.ConfigureField("age", rridx.FieldSetting{Container: rridx.ContainerNameDefault, Parser: parser.NewNumberParser()})
	_ = b.ConfigureField("city", rridx.FieldSetting{Container: rridx.ContainerNameDefault, Parser: parser.NewStrHashParser()})

	docs, cases := genDocs()
	util.PanicIfErr(b.AddDocuments(docs...), "indexing document fail")
	index, err := b.BuildIndexer()
	util.PanicIfErr(err, "gen indexing fail")
	scan := rridx.NewScanner(index)
	for _, c := range cases {
		scan.SetDebug(true)
		ids, err := scan.Retrieve(c.q)
		util.PanicIfErr(err, "retrieve document fail")
		fmt.Println("got:", ids, "expect:", c.expect)
		util.PanicIf(len(ids) != len(c.expect), "result not as expected")
		for _, id := range ids {
			match := util.ContainUint64(c.expect, uint64(id))
			util.PanicIf(!match, "result not match, result:%d not in expect", id)
		}
		scan.Reset()
	}
}

func RunBEIndexing() {
	b := idx.NewIndexerBuilder()
	// b := idx.NewCompactIndexerBuilder()
	docs, cases := genDocs()
	util.PanicIfErr(b.AddDocument(docs...), "indexing doc fail")
	data := b.BuildIndex()
	idx.PrintIndexInfo(data)
	idx.PrintIndexEntries(data)

	for _, c := range cases {
		ids, err := data.Retrieve(c.q) //
		// ids, err := data.Retrieve(c.q, idx.WithDumpEntries(), idx.WithStepDetail()) //
		util.PanicIfErr(err, "query index data fail")
		fmt.Println("got:", ids, "expect:", c.expect)
		util.PanicIf(len(ids) != len(c.expect), "result not as expected")
		for _, id := range ids {
			match := util.ContainUint64(c.expect, uint64(id))
			util.PanicIf(!match, "result not match, result:%d not in expect", id)
		}
	}
}

func main() {
	fmt.Println("start rr indexing =====")
	RunRRIndexing()
	fmt.Println("start be indexing =====")
	RunBEIndexing()
}
