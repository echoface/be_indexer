package engine_test

import (
	"math"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/oracle"
	. "github.com/smartystreets/goconvey/convey"
)

// scoreOnly deliberately hides bounds to exercise the exhaustive-compatible path.
type scoreOnly struct{ core.ScoreSource }

func rankedOracle(t *testing.T, docs []*core.Document, q core.Assignments, k int, src core.ScoreSource, mode core.MissingScoreMode) []core.ScoredDoc {
	t.Helper()
	ids, err := oracle.MatchDocuments(docs, q)
	if err != nil {
		t.Fatal(err)
	}
	var out []core.ScoredDoc
	for _, id := range ids {
		score, ok := 0.0, true
		if src != nil {
			score, ok = src.Score(id)
		}
		if !ok || math.IsNaN(score) {
			if mode == core.MissingScoreSkip {
				continue
			}
			score = 0
		}
		out = append(out, core.ScoredDoc{DocID: id, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].DocID < out[j].DocID
	})
	if k <= 0 {
		return nil
	}
	return out[:min(k, len(out))]
}

func TestTopKShadowAgainstDocuments(t *testing.T) {
	Convey("Top K agrees with raw DNF evaluation across randomized assignments", t, func() {
		rng := rand.New(rand.NewSource(1771))
		schema := simpleFields()
		schema["r"] = core.FieldOption{IndexType: core.IndexNameExtendRange, Encoder: core.IndexNameExtendRange}
		var docs []*core.Document
		var ids []core.DocID
		var scores []float64
		for d := -200; d < 200; d++ {
			id := core.DocID(d * 701)
			doc := core.NewDocument(id)
			for j := 0; j < 1+rng.Intn(4); j++ {
				conj := core.NewConjunction()
				for _, f := range []core.FieldName{"a", "b", "c"} {
					switch rng.Intn(5) {
					case 0:
						conj.Include(f, []int{rng.Intn(6), rng.Intn(6)})
					case 1:
						conj.Exclude(f, rng.Intn(6))
					case 2:
						conj.Include(f, rng.Intn(6))
						conj.Exclude(f, rng.Intn(6))
					}
				}
				if rng.Intn(3) == 0 {
					conj.Between("r", int64(rng.Intn(5)), int64(6+rng.Intn(5)))
				}
				doc.AddConjunction(conj)
			}
			docs = append(docs, doc)
			if rng.Intn(5) != 0 {
				ids = append(ids, id)
				scores = append(scores, float64(rng.Intn(30)-5))
				if d%23 == 0 {
					scores[len(scores)-1] = math.NaN()
				}
			}
		}
		scores[0] = math.Inf(1)
		scores[1] = math.Inf(-1)
		table, err := core.NewBlockMaxScoreTable(ids, scores)
		So(err, ShouldBeNil)
		eng := buildMultiEngine(t, schema, docs, 73)
		defer eng.Close()
		for trial := 0; trial < 240; trial++ {
			q := core.Assignments{}
			for _, f := range []core.FieldName{"a", "b", "c"} {
				if rng.Intn(4) != 0 {
					q[f] = []int{rng.Intn(6), rng.Intn(6)}
				}
			}
			if rng.Intn(3) != 0 {
				q["r"] = rng.Intn(12)
			}
			// Independently protect the unranked K-group merge as required by AGENTS.md.
			full, err := eng.Retrieve(q)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := oracle.MatchDocuments(docs, q)
			if err != nil {
				t.Fatal(err)
			}
			gotIDs := bitmapToSlice(full)
			sort.Sort(gotIDs)
			if !slices.Equal(gotIDs, raw) {
				t.Fatalf("unranked shadow query %v: got %v want %v", q, gotIDs, raw)
			}
			k := []int{0, 1, 7, 30, 500}[trial%5]
			for _, mode := range []core.MissingScoreMode{core.MissingScoreZero, core.MissingScoreSkip} {
				want := rankedOracle(t, docs, q, k, table, mode)
				for _, src := range []core.ScoreSource{scoreOnly{table}, table} {
					c := core.NewTopKCollector(k, src)
					c.SetMissingScoreMode(mode)
					if err := eng.RetrieveWithCollector(q, c); err != nil {
						t.Fatal(err)
					}
					if got := c.Scored(); !reflect.DeepEqual(got, want) {
						t.Fatalf("trial=%d k=%d mode=%d source=%T\ngot=%v\nwant=%v", trial, k, mode, src, got, want)
					}
				}
			}
		}
	})
}

func TestTopKGlobalOrderingAndVisibility(t *testing.T) {
	Convey("late high scores, signed IDs, wildcard excludes and cross-layer duplicates are exact", t, func() {
		docs := []*core.Document{
			core.NewDocument(100).AddConjunction(core.NewConjunction().Exclude("a", 2)),
			core.NewDocument(1).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(-1).AddConjunction(core.NewConjunction().Include("a", 1).Include("b", 1)),
			core.NewDocument(-core.MaxDocID).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(core.MaxDocID).AddConjunction(core.NewConjunction().Include("a", 1)),
		}
		eng := buildSingleEngine(t, simpleFields(), docs)
		defer eng.Close()
		table, err := core.NewBlockMaxScoreTable([]core.DocID{-core.MaxDocID, -1, 1, 100, core.MaxDocID}, []float64{10, 20, 1, 1, 10})
		So(err, ShouldBeNil)
		q := core.Assignments{"a": 1, "b": 1}
		for _, k := range []int{-1, 0, 1, 2, 20} {
			got, err := eng.RetrieveTopK(q, k, table)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, rankedOracle(t, docs, q, k, table, core.MissingScoreZero))
			got, err = eng.RetrieveTopK(q, k, nil)
			So(err, ShouldBeNil)
			So(got, ShouldResemble, rankedOracle(t, docs, q, k, nil, core.MissingScoreZero))
		}
		// The highest-scored full version is stale, and a high-scored delta is
		// deleted. Neither may raise the threshold or consume quota.
		full := buildSingleEngine(t, simpleFields(), []*core.Document{
			core.NewDocument(1).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(2).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(3).AddConjunction(core.NewConjunction().Include("a", 1)),
		})
		oldDelta := buildSingleEngine(t, simpleFields(), []*core.Document{core.NewDocument(2).AddConjunction(core.NewConjunction().Include("a", 1))})
		ld := core.NewDocVisibilityMask()
		ld.Hide(2)
		oldDelta.SetDocVisibilityMask(ld)
		delta := buildSingleEngine(t, simpleFields(), []*core.Document{
			core.NewDocument(2).AddConjunction(core.NewConjunction().Include("a", 2)),
			core.NewDocument(4).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(5).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(6).AddConjunction(core.NewConjunction().Include("a", 1)),
		})
		duplicate := buildSingleEngine(t, simpleFields(), []*core.Document{core.NewDocument(5).AddConjunction(core.NewConjunction().Include("a", 1))})
		ce := engine.NewCompositeEngine(&engine.IndexSnapshot{FullEngine: full, DeltaEngines: []*engine.BooleanEngine{oldDelta, delta, duplicate}, ChangedDocs: core.NewBitmapDocSet(2), DeletedDocs: core.NewBitmapDocSet(4)})
		defer ce.Close()
		source, err := core.NewBlockMaxScoreTable([]core.DocID{1, 2, 3, 4, 5, 6}, []float64{1, 10000, 2, 20000, 10, 9})
		So(err, ShouldBeNil)
		want := []core.ScoredDoc{{DocID: 5, Score: 10}, {DocID: 6, Score: 9}, {DocID: 3, Score: 2}}
		got, err := ce.RetrieveTopK(q, 3, source)
		So(err, ShouldBeNil)
		So(got, ShouldResemble, want)
		c := core.NewTopKCollector(3, source)
		So(ce.RetrieveWithCollector(q, c), ShouldBeNil)
		So(c.Scored(), ShouldResemble, want)
	})
}

type topKObserver struct {
	start   func()
	matches int
}

func (o *topKObserver) OnRetrieveStart(*core.RetrieveContext) {
	if o.start != nil {
		o.start()
	}
}
func (o *topKObserver) OnRetrieveEnd(*core.RetrieveContext)        {}
func (o *topKObserver) OnConjunctionMatch(core.DocID, core.ConjID) { o.matches++ }
func (o *topKObserver) OnExcludeSkip(core.DocID)                   {}
func (o *topKObserver) OnCursorInit(int)                           {}

func TestTopKSnapshotPublication(t *testing.T) {
	Convey("a refresh during retrieval cannot mix scores or bounds, even between full and delta", t, func() {
		full := buildSingleEngine(t, simpleFields(), []*core.Document{core.NewDocument(1).AddConjunction(core.NewConjunction().Include("a", 1))})
		delta := buildSingleEngine(t, simpleFields(), []*core.Document{core.NewDocument(2).AddConjunction(core.NewConjunction().Include("a", 1))})
		ce := engine.NewCompositeEngine(&engine.IndexSnapshot{FullEngine: full, DeltaEngines: []*engine.BooleanEngine{delta}})
		defer ce.Close()
		old, _ := core.NewBlockMaxScoreTable([]core.DocID{1, 2}, []float64{100, 1})
		next, _ := core.NewBlockMaxScoreTable([]core.DocID{1, 2}, []float64{1, 200})
		ce.SetScoreSource(old)
		obs := &topKObserver{start: func() { ce.SetScoreSource(next) }}
		q := core.Assignments{"a": 1}
		got, err := ce.RetrieveTopK(q, 1, nil, func(ctx *core.RetrieveContext) { ctx.Observer = obs })
		So(err, ShouldBeNil)
		So(got, ShouldResemble, []core.ScoredDoc{{DocID: 1, Score: 100}})
		got, err = ce.RetrieveTopK(q, 1, nil)
		So(err, ShouldBeNil)
		So(got, ShouldResemble, []core.ScoredDoc{{DocID: 2, Score: 200}})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				ce.SetScoreSource(old)
				ce.SetScoreSource(next)
			}
		}()
		for i := 0; i < 100; i++ {
			got, err := ce.RetrieveTopK(q, 1, nil)
			So(err, ShouldBeNil)
			So(len(got), ShouldEqual, 1)
			So(got[0] == (core.ScoredDoc{DocID: 1, Score: 100}) || got[0] == (core.ScoredDoc{DocID: 2, Score: 200}), ShouldBeTrue)
		}
		wg.Wait()
	})
}

func TestTopKPrunesBooleanEvaluation(t *testing.T) {
	Convey("a full score heap skips low-ranked documents before matching", t, func() {
		var docs []*core.Document
		var ids []core.DocID
		var scores []float64
		for id := core.DocID(1); id <= 4096; id++ {
			docs = append(docs, core.NewDocument(id).AddConjunction(core.NewConjunction().Include("a", 1).Include("b", 1)))
			ids = append(ids, id)
			scores = append(scores, float64(4097-id))
		}
		eng := buildSingleEngine(t, simpleFields(), docs)
		defer eng.Close()
		table, err := core.NewBlockMaxScoreTable(ids, scores)
		So(err, ShouldBeNil)
		obs := &topKObserver{}
		got, err := eng.RetrieveTopK(core.Assignments{"a": 1, "b": 1}, 10, table, func(ctx *core.RetrieveContext) { ctx.Observer = obs })
		So(err, ShouldBeNil)
		So(len(got), ShouldEqual, 10)
		So(obs.matches, ShouldEqual, 10)
		So(got[0].DocID, ShouldEqual, 1)
		So(got[9].DocID, ShouldEqual, 10)
		obs.matches = 0
		exhaustive, err := eng.RetrieveTopK(core.Assignments{"a": 1, "b": 1}, 10, table, func(ctx *core.RetrieveContext) { ctx.Observer = obs; ctx.DisableScorePruning = true })
		So(err, ShouldBeNil)
		So(exhaustive, ShouldResemble, got)
		So(obs.matches, ShouldEqual, 4096)

	})
}
