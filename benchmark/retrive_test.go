//go:build !legacy_benchmark
// +build !legacy_benchmark

package be_indexer_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/echoface/be_indexer/holder/rangeholder"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
	"github.com/echoface/be_indexer/segmentindex"
	"github.com/echoface/be_indexer/storage"
)

type benchDocument struct {
	ID      int64  `json:"id"`
	Gender  int    `json:"gender"`
	Number1 int64  `json:"number_1"`
	Number2 int64  `json:"number_2"`
	Number3 int64  `json:"number_3"`
	Char1   string `json:"char_1"`
	Char2   string `json:"char_2"`
	Char3   string `json:"char_3"`
	Char4   string `json:"char_4"`
	Char5   string `json:"char_5"`
	Age1    int64  `json:"age_1"`
	Age2    int64  `json:"age_2"`
	Age3    int64  `json:"age_3"`
	Age4    int64  `json:"age_4"`
	Age5    int64  `json:"age_5"`
}

func benchmarkAssignments(doc benchDocument) be_indexer.Assignments {
	return be_indexer.Assignments{
		"gender":   be_indexer.NewIntValues(doc.Gender),
		"number_1": be_indexer.NewInt64Values(doc.Number1),
		"number_2": be_indexer.NewInt64Values(doc.Number2),
		"number_3": be_indexer.NewInt64Values(doc.Number3),
		"char_1":   be_indexer.NewStrValues(doc.Char1),
		"char_2":   be_indexer.NewStrValues(doc.Char2),
		"char_3":   be_indexer.NewStrValues(doc.Char3),
		"char_4":   be_indexer.NewStrValues(doc.Char4),
		"char_5":   be_indexer.NewStrValues(doc.Char5),
		"age_1":    be_indexer.NewInt64Values(doc.Age1),
		"age_2":    be_indexer.NewInt64Values(doc.Age2),
		"age_3":    be_indexer.NewInt64Values(doc.Age3),
		"age_4":    be_indexer.NewInt64Values(doc.Age4),
		"age_5":    be_indexer.NewInt64Values(doc.Age5),
	}
}

func BenchmarkQueryPlannerPlan(b *testing.B) {
	builder := storage.NewSegmentBuilder()
	for _, posting := range []struct {
		field string
		value string
		count int
	}{
		{field: "city", value: "bj", count: 128},
		{field: "device", value: "ios", count: 8},
		{field: "age", value: "30", count: 64},
	} {
		ids := make([]uint64, posting.count)
		for idx := range ids {
			ids[idx] = uint64(idx + 1)
		}
		if err := builder.AddPosting(posting.field, posting.value, ids); err != nil {
			b.Fatalf("add planner benchmark posting %s=%s: %v", posting.field, posting.value, err)
		}
	}
	segment, err := builder.Seal()
	if err != nil {
		b.Fatalf("seal planner benchmark segment: %v", err)
	}
	query := planner.Query{Terms: []planner.TermQuery{
		{Field: "city", Value: "bj", Op: planner.OpAND},
		{Field: "device", Value: "ios", Op: planner.OpAND},
		{Field: "age", Value: "30", Op: planner.OpAND},
	}}
	queryPlanner := planner.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan, planErr := queryPlanner.Plan(segment, query)
		if planErr != nil {
			b.Fatal(planErr)
		}
		benchmarkPlanSink = plan
	}
}

var benchmarkPlanSink planner.Plan

func addBenchmarkDocument(builder *be_indexer.IndexerBuilder, row benchDocument) error {
	return builder.AddDocument(be_indexer.NewDocument(be_indexer.DocID(row.ID)).AddConjunction(benchmarkConjunction(row)))
}

func benchmarkConjunction(row benchDocument) *be_indexer.Conjunction {
	return be_indexer.NewConjunction().
		In("gender", be_indexer.NewIntValues(row.Gender)).
		In("number_1", be_indexer.NewInt64Values(row.Number1)).
		In("number_2", be_indexer.NewInt64Values(row.Number2)).
		In("number_3", be_indexer.NewInt64Values(row.Number3)).
		In("char_1", be_indexer.NewStrValues(row.Char1)).
		In("char_2", be_indexer.NewStrValues(row.Char2)).
		In("char_3", be_indexer.NewStrValues(row.Char3)).
		In("char_4", be_indexer.NewStrValues(row.Char4)).
		In("char_5", be_indexer.NewStrValues(row.Char5)).
		Between("age_1", row.Age1, row.Age1+1).
		Between("age_2", row.Age2, row.Age2+1).
		Between("age_3", row.Age3, row.Age3+1).
		Between("age_4", row.Age4, row.Age4+1).
		Between("age_5", row.Age5, row.Age5+1)
}

func BenchmarkKGroupsBEIndexRetrieveFromJSONL(b *testing.B) {
	path := filepath.Join("..", "static", "testdata", "bench_docs.json")
	file, err := os.Open(path)
	if err != nil {
		b.Fatalf("open benchmark data %q (generate it with python3 static/generate_bench_docs.py): %v", path, err)
	}
	defer file.Close()

	builder := be_indexer.NewIndexerBuilder()
	for _, field := range []string{"gender", "number_1", "number_2", "number_3", "char_1", "char_2", "char_3", "char_4", "char_5"} {
		builder.ConfigField(be_indexer.BEField(field), be_indexer.FieldOption{Container: be_indexer.HolderNameDefault})
	}
	for _, field := range []string{"age_1", "age_2", "age_3", "age_4", "age_5"} {
		builder.ConfigField(be_indexer.BEField(field), be_indexer.FieldOption{Container: be_indexer.HolderNameExtendRange})
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var queryDoc benchDocument
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var row benchDocument
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			b.Fatalf("decode JSONL line %d: %v", lineNumber, err)
		}
		if lineNumber == 1 {
			queryDoc = row
		}
		if err := addBenchmarkDocument(builder, row); err != nil {
			b.Fatalf("index JSONL line %d (document %d): %v", lineNumber, row.ID, err)
		}
	}
	b.Logf("total lines :%d", lineNumber)
	if err := scanner.Err(); err != nil {
		b.Fatalf("read benchmark data: %v", err)
	}
	if lineNumber == 0 {
		b.Fatal("benchmark data file is empty")
	}

	indexer := builder.BuildIndex()
	queries := benchmarkAssignments(queryDoc)
	b.ReportAllocs()
	b.ResetTimer() // Exclude file I/O, JSON decoding, and index construction.
	totalHits := 0
	for i := 0; i < b.N; i++ {
		result, err := indexer.Retrieve(queries)
		if err != nil {
			b.Fatal(fmt.Errorf("retrieve benchmark query: %w", err))
		}
		totalHits += len(result)
		benchmarkQuerySink += uint64(len(result))
	}
	b.StopTimer()
	b.ReportMetric(float64(lineNumber), "docs")
	b.ReportMetric(float64(totalHits)/float64(b.N), "avgHits")
}

var benchmarkQuerySink uint64

// BenchmarkSegmentIndexBEIndexRetrieveFromJSONL uses exactly the same
// documents and query as BenchmarkKGroupsBEIndexRetrieveFromJSONL, so their
// query timings are directly comparable.
func BenchmarkSegmentIndexBEIndexRetrieveFromJSONL(b *testing.B) {
	path := filepath.Join("..", "static", "testdata", "bench_docs.json")
	file, err := os.Open(path)
	if err != nil {
		b.Fatalf("open benchmark data %q: %v", path, err)
	}
	defer file.Close()

	builder := segmentindex.NewBuilder()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var queryDoc benchDocument
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		var row benchDocument
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			b.Fatalf("decode JSONL line %d: %v", lineNumber, err)
		}
		if lineNumber == 1 {
			queryDoc = row
		}
		doc := be_indexer.NewDocument(be_indexer.DocID(row.ID)).AddConjunction(benchmarkConjunction(row))
		if err := builder.AddDocument(doc); err != nil {
			b.Fatalf("index JSONL line %d: %v", lineNumber, err)
		}
	}
	if err := scanner.Err(); err != nil {
		b.Fatalf("read benchmark data: %v", err)
	}
	if lineNumber == 0 {
		b.Fatal("benchmark data file is empty")
	}

	index, err := builder.Seal()
	if err != nil {
		b.Fatalf("seal segment index: %v", err)
	}
	queries := benchmarkAssignments(queryDoc)
	resultBuffer := make([]be_indexer.DocID, 0, 1024)
	b.ReportAllocs()
	b.ResetTimer()
	totalHits := 0
	for i := 0; i < b.N; i++ {
		result, err := index.RetrieveAssignmentsInto(resultBuffer, queries)
		if err != nil {
			b.Fatal(fmt.Errorf("retrieve benchmark query: %w", err))
		}
		resultBuffer = result
		totalHits += len(result)
		benchmarkQuerySink += uint64(len(result))
	}
	b.StopTimer()
	b.ReportMetric(float64(lineNumber), "docs")
	b.ReportMetric(float64(totalHits)/float64(b.N), "avgHits")
}
