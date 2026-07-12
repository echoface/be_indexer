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

	FullEngine  *BooleanEngine
	DeltaEngine *BooleanEngine
	// DeltaEngines contains per-delta engines with older delta versions filtered
	// by later changed_docs. When present, it supersedes DeltaEngine and is needed
	// for correct multi-delta update/update semantics.
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
	for _, deltaEngine := range s.deltaEngines() {
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
	result, err := e.Retrieve(queries, opts...)
	if err != nil {
		return err
	}
	if result == nil || collector == nil {
		return nil
	}
	result.ForEach(func(id core.DocID) {
		collector.Add(id, 0)
	})
	return nil
}

func (s *IndexSnapshot) deltaEngines() []*BooleanEngine {
	if s == nil {
		return nil
	}
	if len(s.DeltaEngines) > 0 {
		return s.DeltaEngines
	}
	if s.DeltaEngine != nil {
		return []*BooleanEngine{s.DeltaEngine}
	}
	return nil
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
	if s.DeltaEngine != nil {
		record(s.DeltaEngine.Close())
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
