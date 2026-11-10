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
	scores   core.AtomicScoreSource
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
func (e *CompositeEngine) Retrieve(queries core.Assignments, opts ...core.QueryOption) (*core.BitmapDocSet, error) {
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

// RetrieveWithCollector streams visible full+delta matches through one shared
// collector. Ordinary collectors receive unique IDs; duplicate-tolerant ones
// own deduplication. CollectStop stops all remaining layers. Score pruning is
// routed by CompetitiveCollector capability, independently of deduplication.
func (e *CompositeEngine) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.QueryOption,
) error {
	if collector == nil {
		return core.ErrNilResultCollector
	}
	if e == nil || e.snapshot == nil {
		return nil
	}
	s := e.snapshot
	filtered := &filteredCollector{next: prepareCollector(collector), deny: s.ChangedDocs}
	var sink core.ResultCollector = filtered
	if next, ok := filtered.next.(core.CompetitiveCollector); ok {
		sink = &competitiveFilteredCollector{filteredCollector: filtered, competitive: next}
	}
	if s.FullEngine != nil {
		if err := s.FullEngine.RetrieveWithCollector(queries, sink, opts...); err != nil {
			return err
		}
	}
	filtered.deny = s.DeletedDocs
	for _, delta := range s.DeltaEngines {
		if filtered.stopped {
			break
		}
		if delta == nil {
			continue
		}
		if err := delta.RetrieveWithCollector(queries, sink, opts...); err != nil {
			return err
		}
	}
	return nil
}

// filteredCollector wraps a downstream collector with a DocID deny set:
// members are silently dropped without consuming TopK quota. It implements
// the full-side (ChangedDocs) and delta-side (DeletedDocs) filters needed by
// RetrieveTopK.
type filteredCollector struct {
	next    core.ResultCollector
	deny    *core.BitmapDocSet
	stopped bool
}

func (*filteredCollector) AcceptsDuplicates() bool { return true }

func (c *filteredCollector) Add(id core.DocID) core.CollectResult {
	if c.stopped {
		return core.CollectStop
	}
	if c.deny != nil && c.deny.Contains(id) {
		return core.CollectContinue
	}
	result := c.next.Add(id)
	c.stopped = result == core.CollectStop
	return result
}

type competitiveFilteredCollector struct {
	*filteredCollector
	competitive core.CompetitiveCollector
}

// NextCandidate preserves the shared collector's certified score skips. Denied
// documents never reach Add, so their scores cannot raise the heap threshold.
func (c *competitiveFilteredCollector) NextCandidate(entry core.EntryID) core.EntryID {
	return c.competitive.NextCandidate(entry)
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
