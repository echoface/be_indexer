package rangeholder

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"math/rand"
	"testing"

	"github.com/echoface/be_indexer"
)

func BenchmarkRangeHolder_Build(b *testing.B) {
	docCounts := []int{1000, 10000}
	holders := []string{be_indexer.HolderNameExtendRange, "optimized_range"}

	for _, count := range docCounts {
		for _, holderName := range holders {
			b.Run(fmt.Sprintf("%s_%d", holderName, count), func(b *testing.B) {
				// Prepare docs
				docs := make([]*core.Document, count)
				for i := 0; i < count; i++ {
					doc := core.NewDocument(core.DocID(i))
					// Randomly assign range
					// 33% LT, 33% GT, 33% Between
					r := rand.Intn(3)
					conj := core.NewConjunction()
					if r == 0 {
						conj.LessThan("age", int64(rand.Intn(100000)))
					} else if r == 1 {
						conj.GreaterThan("age", int64(rand.Intn(100000)))
					} else {
						start := rand.Intn(90000)
						conj.Between("age", int64(start), int64(start+rand.Intn(10000)))
					}
					doc.AddConjunction(conj)
					docs[i] = doc
				}

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					builder := be_indexer.NewCompactIndexerBuilder()
					builder.ConfigField("age", core.FieldOption{Container: holderName})
					for _, doc := range docs {
						builder.AddDocument(doc)
					}
					_, _ = builder.BuildIndex()
				}
			})
		}
	}
}

func BenchmarkRangeHolder_Mixed(b *testing.B) {
	docCounts := []int{10000}
	holders := []string{be_indexer.HolderNameExtendRange, "optimized_range"}

	for _, count := range docCounts {
		for _, holderName := range holders {
			b.Run(fmt.Sprintf("%s_%d_90pct_EQ", holderName, count), func(b *testing.B) {
				builder := be_indexer.NewCompactIndexerBuilder()
				builder.ConfigField("age", core.FieldOption{Container: holderName})

				for i := 0; i < count; i++ {
					doc := core.NewDocument(core.DocID(i))
					// 90% EQ, 10% Range
					if rand.Float64() < 0.9 {
						// EQ
						doc.AddConjunction(core.NewConjunction().In("age", []int{rand.Intn(100000)}))
					} else {
						// Range
						r := rand.Intn(3)
						conj := core.NewConjunction()
						if r == 0 {
							conj.LessThan("age", int64(rand.Intn(100000)))
						} else if r == 1 {
							conj.GreaterThan("age", int64(rand.Intn(100000)))
						} else {
							start := rand.Intn(90000)
							conj.Between("age", int64(start), int64(start+rand.Intn(10000)))
						}
						doc.AddConjunction(conj)
					}
					builder.AddDocument(doc)
				}
				indexer, _ := builder.BuildIndex()

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// Random query
					val := rand.Intn(100000)
					_, _ = indexer.Retrieve(core.Assignments{"age": val})
				}
			})
		}
	}
}

func BenchmarkRangeHolder_Retrieve(b *testing.B) {
	docCounts := []int{10000}
	holders := []string{be_indexer.HolderNameExtendRange, "optimized_range"}

	for _, count := range docCounts {
		for _, holderName := range holders {
			b.Run(fmt.Sprintf("%s_%d", holderName, count), func(b *testing.B) {
				// 1. Build Index once
				builder := be_indexer.NewCompactIndexerBuilder()
				builder.ConfigField("age", core.FieldOption{Container: holderName})

				for i := 0; i < count; i++ {
					doc := core.NewDocument(core.DocID(i))
					r := rand.Intn(3)
					conj := core.NewConjunction()
					if r == 0 {
						conj.LessThan("age", int64(rand.Intn(100000)))
					} else if r == 1 {
						conj.GreaterThan("age", int64(rand.Intn(100000)))
					} else {
						start := rand.Intn(90000)
						conj.Between("age", int64(start), int64(start+rand.Intn(10000)))
					}
					doc.AddConjunction(conj)
					builder.AddDocument(doc)
				}
				indexer, _ := builder.BuildIndex()

				// 2. Retrieve
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					// Random query
					val := rand.Intn(100000)
					_, _ = indexer.Retrieve(core.Assignments{"age": val})
				}
			})
		}
	}
}
