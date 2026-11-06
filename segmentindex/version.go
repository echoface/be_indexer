package segmentindex

import (
	be "github.com/echoface/be_indexer"
)

// tombstoneSet records Delete/Update operations as per-doc sequence numbers.
// A tombstone with sequence t kills exactly those doc versions whose add
// sequence is smaller than t, so a doc re-added (or updated) after its delete
// stays visible: Update(id, doc) is Delete(id) followed by AddDocument(doc).
type tombstoneSet struct {
	seqs map[be.DocID]uint64
}

func newTombstoneSet() *tombstoneSet {
	return &tombstoneSet{seqs: make(map[be.DocID]uint64)}
}

// kill marks every version of id added before seq dead.
func (t *tombstoneSet) kill(id be.DocID, seq uint64) {
	if seq > t.seqs[id] {
		t.seqs[id] = seq
	}
}

// dead reports whether the doc version id@seq is tombstoned.
func (t *tombstoneSet) dead(id uint64, seq uint64) bool {
	if t == nil {
		return false
	}
	tombSeq, ok := t.seqs[be.DocID(id)]
	return ok && tombSeq > seq
}

func (t *tombstoneSet) clone() *tombstoneSet {
	if t == nil {
		return newTombstoneSet()
	}
	out := &tombstoneSet{seqs: make(map[be.DocID]uint64, len(t.seqs))}
	for id, seq := range t.seqs {
		out.seqs[id] = seq
	}
	return out
}

func (t *tombstoneSet) len() int {
	if t == nil {
		return 0
	}
	return len(t.seqs)
}

// prune drops tombstones that cannot kill any remaining doc version. minSeq
// maps docID to the smallest add sequence still present in the store (sealed
// deltas plus the pending buffer). A tombstone is kept when the doc has a
// version older than it (still dead), or a version in the pending buffer whose
// fate is decided at seal time.
func (t *tombstoneSet) prune(minSeq map[uint64]uint64, pendingIDs map[uint64]struct{}) {
	for id, tombSeq := range t.seqs {
		if _, pending := pendingIDs[uint64(id)]; pending {
			continue
		}
		if seq, ok := minSeq[uint64(id)]; ok && seq < tombSeq {
			continue
		}
		delete(t.seqs, id)
	}
}
