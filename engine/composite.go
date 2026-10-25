package engine

import (
	"github.com/echoface/be_indexer/core"
)

// IndexSnapshot is an immutable serving view composed from a long-window full
// index and an optional short-window delta index.
//
// Query semantics:
//
//	Result = (FullResult - ChangedDocs) ∪ (DeltaResult - DeletedDocs)
//
// ChangedDocs contains every DocID that has any mutation in the delta window
// (create/update/delete/recreate). DeletedDocs contains DocIDs whose final
// state in this snapshot is deleted. Delta documents should be pre-compacted by
// DocID/version before building the delta index.
type IndexSnapshot struct {
	Generation uint64

	FullEngine *BooleanEngine
	// DeltaEngines contains per-delta engines with older delta versions filtered
	// by later changed_docs.
	DeltaEngines []*BooleanEngine

	ChangedDocs *core.BitmapDocSet
	DeletedDocs *core.BitmapDocSet
}

// CompositeEngine executes queries against an immutable full+delta snapshot.
type CompositeEngine struct {
	snapshot *IndexSnapshot
}

// NewCompositeEngine creates a full+delta query engine from snapshot.
func NewCompositeEngine(snapshot *IndexSnapshot) *CompositeEngine {
	return &CompositeEngine{snapshot: snapshot}
}

// Snapshot returns the immutable snapshot used by this engine.
func (e *CompositeEngine) Snapshot() *IndexSnapshot {
	if e == nil {
		return nil
	}
	return e.snapshot
}

// Retrieve returns matched DocIDs as a BitmapDocSet for assignments.
//
// Query semantics: Result = (FullResult - ChangedDocs) ∪ (DeltaResult - DeletedDocs)
//
// All filtering and merging uses batch bitmap set operations (AndNot, Or)
// instead of per-document loops.
func (e *CompositeEngine) Retrieve(queries core.Assignments, opts ...core.IndexOpt) (*core.BitmapDocSet, error) {
	if e == nil || e.snapshot == nil {
		return core.NewBitmapDocSet(), nil
	}

	s := e.snapshot

	// FullEngine.Retrieve already returns an independent, mutable bitmap, so we
	// reuse it directly as the accumulator (no extra clone/copy). When there is
	// no full engine, start from an empty set.
	var fullSet *core.BitmapDocSet
	if s.FullEngine != nil {
		fullResult, err := s.FullEngine.Retrieve(queries, opts...)
		if err != nil {
			return nil, err
		}
		fullSet = fullResult
	} else {
		fullSet = core.NewBitmapDocSet()
	}

	// Merge every delta engine's result. Like fullSet, the first delta result
	// is reused directly as the accumulator; subsequent ones are Or'ed in.
	// With no delta engine, deltaSet stays nil (Or/AndNot are nil-safe).
	var deltaSet *core.BitmapDocSet
	for _, deltaEngine := range s.DeltaEngines {
		deltaResult, err := deltaEngine.Retrieve(queries, opts...)
		if err != nil {
			return nil, err
		}
		if deltaSet == nil {
			deltaSet = deltaResult
		} else {
			deltaSet.Or(deltaResult)
		}
	}

	// Batch filter: FullResult - ChangedDocs
	if s.ChangedDocs.Cardinality() > 0 {
		fullSet.AndNot(s.ChangedDocs)
	}
	// Batch filter: DeltaResult - DeletedDocs
	if s.DeletedDocs.Cardinality() > 0 {
		deltaSet.AndNot(s.DeletedDocs)
	}

	// Merge delta into full. Since ChangedDocs already removed full's stale
	// entries, delta naturally has priority for updates.
	fullSet.Or(deltaSet)
	return fullSet, nil
}

// RetrieveWithCollector feeds the final de-duplicated full+delta result into collector.
func (e *CompositeEngine) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt,
) error {
	if collector == nil {
		return core.ErrNilResultCollector
	}
	result, err := e.Retrieve(queries, opts...)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	result.ForEach(func(id core.DocID) { _ = collector.Add(id) })
	return nil
}

// filteredCollector wraps a downstream collector with a DocID deny set:
// members are silently dropped without consuming TopK quota. It implements
// the full-side (ChangedDocs) and delta-side (DeletedDocs) filters needed by
// RetrieveTopK.
type filteredCollector struct {
	next core.ResultCollector
	deny *core.BitmapDocSet
}

func (c filteredCollector) Add(id core.DocID) core.CollectResult {
	if c.deny != nil && c.deny.Contains(id) {
		return core.CollectContinue
	}
	return c.next.Add(id)
}

// RetrieveTopK returns at most k documents ranked by descending score (ties:
// ascending DocID). The full engine and every delta engine are queried with
// their own TopK collector, then the per-engine top-k lists are merged into
// the global top-k. Filtering by ChangedDocs/DeletedDocs happens before an
// Add reaches the collector, so stale documents never consume k-quota.
//
// TopK(F' ∪ D') = merge(TopK(F'), TopK(D')) holds for all engines, which is
// what makes the per-engine early termination safe.
func (e *CompositeEngine) RetrieveTopK(
	queries core.Assignments, k int, scores core.ScoreSource, opts ...core.IndexOpt,
) ([]core.ScoredDoc, error) {
	if e == nil || e.snapshot == nil {
		return nil, nil
	}
	s := e.snapshot

	var fullTop []core.ScoredDoc
	if s.FullEngine != nil {
		c := core.NewTopKCollector(k, scores)
		wrapped := filteredCollector{next: c, deny: s.ChangedDocs}
		if err := s.FullEngine.RetrieveWithCollector(queries, wrapped, opts...); err != nil {
			return nil, err
		}
		fullTop = c.Scored()
	}

	var deltaTop []core.ScoredDoc
	for _, deltaEngine := range s.DeltaEngines {
		c := core.NewTopKCollector(k, scores)
		wrapped := filteredCollector{next: c, deny: s.DeletedDocs}
		if err := deltaEngine.RetrieveWithCollector(queries, wrapped, opts...); err != nil {
			return nil, err
		}
		deltaTop = mergeScoredDesc(deltaTop, c.Scored())
	}

	return mergeScoredDescLimit(fullTop, deltaTop, k), nil
}

func mergeScoredDesc(a, b []core.ScoredDoc) []core.ScoredDoc {
	return mergeScoredDescLimit(a, b, len(a)+len(b))
}

func mergeScoredDescLimit(a, b []core.ScoredDoc, limit int) []core.ScoredDoc {
	out := make([]core.ScoredDoc, 0, min(limit, len(a)+len(b)))
	i, j := 0, 0
	for len(out) < limit && (i < len(a) || j < len(b)) {
		switch {
		case j >= len(b) || (i < len(a) && scoredDescLess(a[i], b[j])):
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	return out
}

func scoredDescLess(a, b core.ScoredDoc) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	return a.DocID < b.DocID
}

// Close releases all engines (and their segment readers) owned by this snapshot.
// Callers must ensure no in-flight query still references the snapshot.
func (s *IndexSnapshot) Close() error {
	if s == nil {
		return nil
	}
	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.FullEngine != nil {
		record(s.FullEngine.Close())
	}
	for _, de := range s.DeltaEngines {
		record(de.Close())
	}
	return firstErr
}

// Close releases the underlying snapshot resources.
func (e *CompositeEngine) Close() error {
	if e == nil {
		return nil
	}
	return e.snapshot.Close()
}
