package engine_test

import (
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	. "github.com/smartystreets/goconvey/convey"
)

type stoppingCollector struct {
	ids   []core.DocID
	limit int
}

func (c *stoppingCollector) Add(id core.DocID) core.CollectResult {
	c.ids = append(c.ids, id)
	if c.limit > 0 && len(c.ids) >= c.limit {
		return core.CollectStop
	}
	return core.CollectContinue
}

// This custom collector deliberately implements only pruning, not duplicate
// tolerance. It must retain pruning through the engine's dedup/filter adapters.
type customCompetitiveCollector struct {
	stoppingCollector
	skips int
}

func (c *customCompetitiveCollector) NextCandidate(entry core.EntryID) core.EntryID {
	c.skips++
	return entry
}

type layerObserver struct{ starts int }

func (o *layerObserver) OnRetrieveStart(*core.RetrieveContext)    { o.starts++ }
func (*layerObserver) OnRetrieveEnd(*core.RetrieveContext)        {}
func (*layerObserver) OnConjunctionMatch(core.DocID, core.ConjID) {}
func (*layerObserver) OnExcludeSkip(core.DocID)                   {}
func (*layerObserver) OnCursorInit(int)                           {}

func TestCollectorContractAcrossEngines(t *testing.T) {
	Convey("ordinary collectors see unique IDs and stop the complete retrieval", t, func() {
		doc := func(id core.DocID) *core.Document {
			return core.NewDocument(id).AddConjunction(
				core.NewConjunction().Include("a", 1),
				core.NewConjunction().Include("a", 1))
		}
		full := buildCompositeTestEngine(t, []*core.Document{doc(1), doc(2)})
		delta := buildCompositeTestEngine(t, []*core.Document{doc(2), doc(3)})
		ce := engine.NewCompositeEngine(&engine.IndexSnapshot{FullEngine: full, DeltaEngines: []*engine.BooleanEngine{delta}})
		defer ce.Close()
		query := core.Assignments{"a": 1}
		for _, subject := range []struct {
			name     string
			retrieve func(core.Assignments, core.ResultCollector, ...core.QueryOption) error
			count    int
		}{{"single", full.RetrieveWithCollector, 2}, {"composite", ce.RetrieveWithCollector, 3}} {
			Convey(subject.name, func() {
				all := &stoppingCollector{}
				So(subject.retrieve(query, all), ShouldBeNil)
				So(len(all.ids), ShouldEqual, subject.count)
				stop := &stoppingCollector{limit: 1}
				observer := &layerObserver{}
				So(subject.retrieve(query, stop, withObserver(observer)), ShouldBeNil)
				So(len(stop.ids), ShouldEqual, 1)
				So(observer.starts, ShouldEqual, 1)
				custom := &customCompetitiveCollector{}
				So(subject.retrieve(query, custom), ShouldBeNil)
				So(len(custom.ids), ShouldEqual, subject.count)
				So(custom.skips, ShouldBeGreaterThan, 0)
			})
		}
	})
	Convey("denied full IDs do not consume quota or suppress the delta replacement", t, func() {
		full := buildCompositeTestEngine(t, []*core.Document{core.NewDocument(1).AddConjunction(core.NewConjunction().Include("a", 1))})
		delta := buildCompositeTestEngine(t, []*core.Document{
			core.NewDocument(1).AddConjunction(core.NewConjunction().Include("a", 1)),
			core.NewDocument(2).AddConjunction(core.NewConjunction().Include("a", 1)),
		})
		ce := engine.NewCompositeEngine(&engine.IndexSnapshot{FullEngine: full, DeltaEngines: []*engine.BooleanEngine{delta}, ChangedDocs: core.NewBitmapDocSet(1), DeletedDocs: core.NewBitmapDocSet(2)})
		defer ce.Close()
		c := &stoppingCollector{limit: 1}
		So(ce.RetrieveWithCollector(core.Assignments{"a": 1}, c), ShouldBeNil)
		So(c.ids, ShouldResemble, []core.DocID{1})
	})
}
