package be_indexer_test

import (
	"fmt"
	"math/rand"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
)

const (
	phase0BenchmarkSeed       int64 = 20261104
	phase0BenchmarkDocCount         = 10000
	phase0BenchmarkQueryCount       = 256
)

var phase0BenchmarkFields = []be.BEField{"A", "B", "C", "D"}

type phase0BenchmarkDataset struct {
	docs    []*be.Document
	queries []be.Assignments
}

func makePhase0BenchmarkDataset() phase0BenchmarkDataset {
	rng := rand.New(rand.NewSource(phase0BenchmarkSeed))
	dataset := phase0BenchmarkDataset{
		docs:    make([]*be.Document, 0, phase0BenchmarkDocCount),
		queries: make([]be.Assignments, 0, phase0BenchmarkQueryCount),
	}
	for id := 1; id <= phase0BenchmarkDocCount; id++ {
		conj := be.NewConjunction()
		for _, field := range phase0BenchmarkFields {
			if rng.Intn(5) == 0 {
				continue
			}
			value := rng.Intn(128)
			if rng.Intn(2) == 0 {
				conj.In(field, be.NewIntValues(value))
			} else {
				conj.NotIn(field, be.NewIntValues(value))
			}
		}
		dataset.docs = append(dataset.docs, be.NewDocument(be.DocID(id)).AddConjunction(conj))
	}
	for i := 0; i < phase0BenchmarkQueryCount; i++ {
		query := make(be.Assignments, len(phase0BenchmarkFields))
		for _, field := range phase0BenchmarkFields {
			query[field] = be.NewIntValues(rng.Intn(128))
		}
		dataset.queries = append(dataset.queries, query)
	}
	return dataset
}

func buildDefaultBenchmarkIndex(b *testing.B, docs []*be.Document) be.BEIndex {
	b.Helper()
	builder := be.NewIndexerBuilder()
	for _, field := range phase0BenchmarkFields {
		builder.ConfigField(field, be.FieldOption{Container: be.HolderNameDefault})
	}
	if err := builder.AddDocument(docs...); err != nil {
		b.Fatal(err)
	}
	return builder.BuildIndex()
}

func buildRoaringBenchmarkIndex(b *testing.B, docs []*be.Document) *roaringidx.IvtBEIndexer {
	b.Helper()
	builder := roaringidx.NewIndexerBuilder()
	for _, field := range phase0BenchmarkFields {
		if err := builder.ConfigureField(string(field), roaringidx.FieldSetting{
			Container: roaringidx.ContainerNameDefault,
			Parser:    parser.NewNumberParser(),
		}); err != nil {
			b.Fatal(err)
		}
	}
	if err := builder.AddDocuments(docs...); err != nil {
		b.Fatal(err)
	}
	index, err := builder.BuildIndexer()
	if err != nil {
		b.Fatal(err)
	}
	return index
}

func BenchmarkPhase0BuildDefault(b *testing.B) {
	dataset := makePhase0BenchmarkDataset()
	b.ReportMetric(float64(len(dataset.docs)), "docs")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		builder := be.NewIndexerBuilder()
		for _, field := range phase0BenchmarkFields {
			builder.ConfigField(field, be.FieldOption{Container: be.HolderNameDefault})
		}
		if err := builder.AddDocument(dataset.docs...); err != nil {
			b.Fatal(err)
		}
		_ = builder.BuildIndex()
	}
}

func BenchmarkPhase0BuildRoaring(b *testing.B) {
	dataset := makePhase0BenchmarkDataset()
	b.ReportMetric(float64(len(dataset.docs)), "docs")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		builder := roaringidx.NewIndexerBuilder()
		for _, field := range phase0BenchmarkFields {
			if err := builder.ConfigureField(string(field), roaringidx.FieldSetting{
				Container: roaringidx.ContainerNameDefault,
				Parser:    parser.NewNumberParser(),
			}); err != nil {
				b.Fatal(err)
			}
		}
		if err := builder.AddDocuments(dataset.docs...); err != nil {
			b.Fatal(err)
		}
		if _, err := builder.BuildIndexer(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPhase0RetrieveDefault(b *testing.B) {
	dataset := makePhase0BenchmarkDataset()
	index := buildDefaultBenchmarkIndex(b, dataset.docs)
	b.ReportMetric(float64(len(dataset.docs)), "docs")
	b.ReportMetric(float64(len(dataset.queries)), "queries")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := index.Retrieve(dataset.queries[i%len(dataset.queries)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPhase0RetrieveRoaring(b *testing.B) {
	dataset := makePhase0BenchmarkDataset()
	index := buildRoaringBenchmarkIndex(b, dataset.docs)
	scanner := roaringidx.NewScanner(index)
	b.ReportMetric(float64(len(dataset.docs)), "docs")
	b.ReportMetric(float64(len(dataset.queries)), "queries")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.Reset()
		if _, err := scanner.Retrieve(dataset.queries[i%len(dataset.queries)]); err != nil {
			b.Fatal(err)
		}
	}
}

func Example_phase0BaselineCommands() {
	fmt.Println("go test ./... -run TestPhase0")
	fmt.Println("go test ./benchmark -run '^$' -bench Phase0 -benchmem")
	// Output:
	// go test ./... -run TestPhase0
	// go test ./benchmark -run '^$' -bench Phase0 -benchmem
}
