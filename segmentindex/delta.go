package segmentindex

// DeltaID identifies one immutable delta batch inside a Store.
type DeltaID uint64

// Delta is an immutable batch of conjunction groups sealed from the pending
// buffer (or produced by compaction). Deltas are append-only until compaction
// replaces them; queries read them through a published store view and never
// observe partial state.
type Delta struct {
	id     DeltaID
	index  *Index
	groups int // conjunction groups in index
	docs   int // doc versions referenced by the groups
}

func newDelta(id DeltaID, index *Index) *Delta {
	docs := 0
	for _, meta := range index.groups {
		docs += len(meta.seqs)
	}
	return &Delta{id: id, index: index, groups: len(index.groups), docs: docs}
}

// ID returns the delta identity.
func (d *Delta) ID() DeltaID {
	if d == nil {
		return 0
	}
	return d.id
}

// Docs returns how many doc versions the delta holds (tombstoned included).
func (d *Delta) Docs() int {
	if d == nil {
		return 0
	}
	return d.docs
}

// Groups returns how many conjunction groups the delta holds.
func (d *Delta) Groups() int {
	if d == nil {
		return 0
	}
	return d.groups
}
