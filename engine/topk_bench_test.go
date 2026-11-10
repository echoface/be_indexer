package engine_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/echoface/be_indexer/core"
)

// Hiding CompetitiveCollector gives a true exhaustive Boolean + heap baseline.
type exhaustiveTopK struct{ collector *core.TopKCollector }

func (c exhaustiveTopK) Add(id core.DocID) core.CollectResult { return c.collector.Add(id) }
func (exhaustiveTopK) AcceptsDuplicates() bool                { return true }

// BenchmarkTopK compares all-match / selective retrieval and scores correlated,
// independent, or anti-correlated with posting order. Query setup is included;
// collectors are reused and result-copy/sort is outside the timed loop.
func BenchmarkTopK(b *testing.B) {
	const n = 100000
	schema := core.Schema{"a": {Encoder: "number"}, "b": {Encoder: "number"}, "c": {Encoder: "number"}, "d": {Encoder: "number"}}
	for _, every := range []int{1, 10, 1000} {
		b.Run(fmt.Sprintf("match_1_in_%d", every), func(b *testing.B) {
			docs := make([]*core.Document, 0, n)
			ids := make([]core.DocID, n)
			for i := 0; i < n; i++ {
				id := core.DocID(i + 1)
				ids[i] = id
				docs = append(docs, core.NewDocument(id).AddConjunction(core.NewConjunction().Include("a", 1).Include("b", i%every).Include("c", 1).Include("d", 1)))
			}
			eng := buildBenchEngine(b, schema, docs)
			defer eng.Close()
			query := core.Assignments{"a": 1, "b": 0, "c": 1, "d": 1}
			for _, distribution := range []string{"early_high", "random", "late_high"} {
				b.Run(distribution, func(b *testing.B) {
					values := make([]float64, n)
					rng := rand.New(rand.NewSource(42))
					for i := range values {
						switch distribution {
						case "early_high":
							values[i] = float64(n - i)
						case "late_high":
							values[i] = float64(i + 1)
						default:
							values[i] = 1 + rng.Float64()
						}
					}
					dense := core.NewDenseScoreTable(1, values)
					blocks, err := core.NewBlockMaxScoreTable(ids, values)
					if err != nil {
						b.Fatal(err)
					}
					for _, k := range []int{10, 100} {
						b.Run(fmt.Sprintf("top_%d", k), func(b *testing.B) {
							reference := core.NewTopKCollector(k, dense)
							if err := eng.RetrieveWithCollector(query, exhaustiveTopK{reference}); err != nil {
								b.Fatal(err)
							}
							want := reference.Scored()
							for _, strategy := range []string{"full_scan", "score_check", "block_max"} {
								b.Run(strategy, func(b *testing.B) {
									var src core.ScoreSource = dense
									if strategy == "block_max" {
										src = blocks
									}
									collector := core.NewTopKCollector(k, src)
									var sink core.ResultCollector = collector
									if strategy == "full_scan" {
										sink = exhaustiveTopK{collector}
									}
									observer := &topKObserver{}
									if err := eng.RetrieveWithCollector(query, sink, func(ctx *core.RetrieveContext) { ctx.Observer = observer }); err != nil {
										b.Fatal(err)
									}
									if !reflect.DeepEqual(collector.Scored(), want) {
										b.Fatal("ranking differs from exhaustive retrieval")
									}
									b.ReportAllocs()
									b.ResetTimer()
									for i := 0; i < b.N; i++ {
										collector.Reset(src)
										if err := eng.RetrieveWithCollector(query, sink); err != nil {
											b.Fatal(err)
										}
									}
									b.StopTimer()
									b.ReportMetric(float64(observer.matches), "matches/op")
								})
							}
						})
					}
				})
			}
		})
	}
}
