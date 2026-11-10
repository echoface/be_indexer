package engine

import "github.com/echoface/be_indexer/core"

// zeroScoreSource makes RetrieveTopK with no published scores an exact global
// ranking by ascending signed DocID. NewTopKCollector(k,nil) separately retains
// its legacy first-k-hit contract for callers that explicitly want early stop.
type zeroScoreSource struct{}

func (zeroScoreSource) Score(core.DocID) (float64, bool) { return 0, true }

// SetScoreSource atomically publishes a complete immutable score sidecar.
// It can run concurrently with retrieval; each query uses one snapshot. Periodic
// fetching/building belongs to the caller, so failed refreshes retain old scores.
func (e *BooleanEngine) SetScoreSource(scores core.ScoreSource)   { e.scores.Store(scores) }
func (e *CompositeEngine) SetScoreSource(scores core.ScoreSource) { e.scores.Store(scores) }

// RetrieveTopK returns the exact best k matching documents, sorted by descending
// score then ascending signed DocID. An explicit source overrides the published
// sidecar. With nil, the published snapshot is read once; absent scores use zero.
// k <= 0 returns no results. For reusable storage or MissingScoreSkip, supply a
// TopKCollector to RetrieveWithCollector instead.
func (e *BooleanEngine) RetrieveTopK(queries core.Assignments, k int, scores core.ScoreSource, opts ...core.QueryOption) ([]core.ScoredDoc, error) {
	if k <= 0 {
		return nil, nil
	}
	if scores == nil {
		scores = e.scores.Snapshot()
	}
	if scores == nil {
		scores = zeroScoreSource{}
	}
	c := core.NewTopKCollector(k, scores)
	if err := e.RetrieveWithCollector(queries, c, opts...); err != nil {
		return nil, err
	}
	return c.Scored(), nil
}

// RetrieveTopK uses one score snapshot and one heap across full and every delta.
// Visibility filters run before collection; stale/deleted versions cannot fill
// quota or raise the threshold. Winners are deduplicated across all layers.
func (e *CompositeEngine) RetrieveTopK(queries core.Assignments, k int, scores core.ScoreSource, opts ...core.QueryOption) ([]core.ScoredDoc, error) {
	if k <= 0 || e == nil || e.snapshot == nil {
		return nil, nil
	}
	if scores == nil {
		scores = e.scores.Snapshot()
	}
	if scores == nil {
		scores = zeroScoreSource{}
	}
	c := core.NewTopKCollector(k, scores)
	if err := e.RetrieveWithCollector(queries, c, opts...); err != nil {
		return nil, err
	}
	return c.Scored(), nil
}
