package engine

import (
	"github.com/RoaringBitmap/roaring/roaring64"

	"github.com/echoface/be_indexer/core"
)

// DocSet is a read-only set of DocIDs used by the full+delta merge layer.
//
// It deliberately lives above BooleanEngine: a single BooleanEngine-level
// LiveDocs filter cannot represent update/recreate semantics across full and
// delta indexes, because it would filter the same DocID in both layers.
type DocSet interface {
	Contains(id core.DocID) bool
}

// BitmapDocSet is a compact roaring-backed DocID set.
type BitmapDocSet struct {
	bits *roaring64.Bitmap
}

// NewBitmapDocSet creates a DocID set initialized with ids.
func NewBitmapDocSet(ids ...core.DocID) *BitmapDocSet {
	s := &BitmapDocSet{bits: roaring64.New()}
	for _, id := range ids {
		s.Add(id)
	}
	return s
}

// Add inserts a DocID into the set.
func (s *BitmapDocSet) Add(id core.DocID) {
	if s == nil {
		return
	}
	s.bits.Add(uint64(id))
}

// Remove deletes a DocID from the set.
func (s *BitmapDocSet) Remove(id core.DocID) {
	if s == nil {
		return
	}
	s.bits.Remove(uint64(id))
}

// Contains reports whether id is present.
func (s *BitmapDocSet) Contains(id core.DocID) bool {
	return s != nil && s.bits.Contains(uint64(id))
}

// Cardinality returns the number of DocIDs in the set.
func (s *BitmapDocSet) Cardinality() uint64 {
	if s == nil {
		return 0
	}
	return s.bits.GetCardinality()
}

// ForEach calls fn for every DocID in the set.
func (s *BitmapDocSet) ForEach(fn func(core.DocID)) {
	if s == nil || fn == nil {
		return
	}
	it := s.bits.Iterator()
	for it.HasNext() {
		fn(core.DocID(it.Next()))
	}
}

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

	ChangedDocs DocSet
	DeletedDocs DocSet
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

// Retrieve returns merged DocIDs for assignments.
func (e *CompositeEngine) Retrieve(queries core.Assignments, opts ...core.IndexOpt) (core.DocIDList, error) {
	collector := core.PickCollector()
	defer core.PutCollector(collector)
	if err := e.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}
	return collector.GetDocIDs(), nil
}

// RetrieveWithCollector feeds the final de-duplicated full+delta result into collector.
func (e *CompositeEngine) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt,
) error {
	if e == nil || e.snapshot == nil {
		return nil
	}
	if collector == nil {
		return nil
	}

	s := e.snapshot
	merged := core.PickCollector()
	defer core.PutCollector(merged)

	if s.FullEngine != nil {
		fullIDs, err := s.FullEngine.Retrieve(queries, opts...)
		if err != nil {
			return err
		}
		for _, id := range fullIDs {
			if s.ChangedDocs != nil && s.ChangedDocs.Contains(id) {
				continue
			}
			merged.Add(id, 0)
		}
	}

	for _, deltaEngine := range s.deltaEngines() {
		deltaIDs, err := deltaEngine.Retrieve(queries, opts...)
		if err != nil {
			return err
		}
		for _, id := range deltaIDs {
			if s.DeletedDocs != nil && s.DeletedDocs.Contains(id) {
				continue
			}
			// The merged collector de-duplicates DocID. Since full changed docs are
			// removed before this point, delta naturally has priority for updates.
			merged.Add(id, 0)
		}
	}

	ids := merged.GetDocIDs()
	for _, id := range ids {
		collector.Add(id, 0)
	}
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
