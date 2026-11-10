package core

import (
	"fmt"
	"math"
	"sort"
)

// CompetitiveScoreSource augments an immutable score snapshot with certified
// EntryID skips. The same snapshot MUST supply both Score and the bound.
type CompetitiveScoreSource interface {
	ScoreSource
	// No document in [entry, returned target) may outrank threshold under missing.
	// A conservative implementation may return entry; EndEntryID means none remain.
	NextCompetitive(entry EntryID, threshold ScoredDoc, missing MissingScoreMode) EntryID
}

const scoreBlockSize = 64
const scoreDocKeyMask uint64 = (1 << 44) - 1

// BlockMaxScoreTable is an index-independent score sidecar. Scores are ordered
// by EntryID's sign-magnitude DocID key, with a tree of best (score, signed ID)
// bounds over 64-document blocks. It works with any PostingIterator/container
// and never changes the segment format. Construction copies all input slices;
// publication via AtomicScoreSource is safe while previous queries finish.
// Storage is ~16 bytes/scored document plus <1 byte/document for tree bounds.
type BlockMaxScoreTable struct {
	keys      []uint64
	scores    []float64
	best      []ScoredDoc
	leafBase  int
	denseKeys bool
}

// NewBlockMaxScoreTable accepts strictly ascending signed IDs. NaN is absent;
// positive/negative infinity are valid scores. The producer may supply a subset
// or superset of indexed documents. MissingScoreZero conservatively disables
// interval pruning at thresholds <= 0, where unlisted IDs might still win.
func NewBlockMaxScoreTable(ids []DocID, scores []float64) (*BlockMaxScoreTable, error) {
	if len(ids) != len(scores) {
		return nil, fmt.Errorf("score table: ids and scores length mismatch")
	}
	present := 0
	for i, id := range ids {
		if !math.IsNaN(scores[i]) {
			present++
		}
		if !ValidDocID(id) {
			return nil, fmt.Errorf("score table: invalid DocID %d", id)
		}
		if i > 0 && id <= ids[i-1] {
			return nil, fmt.Errorf("score table: ids must be strictly ascending")
		}
	}
	t := &BlockMaxScoreTable{keys: make([]uint64, 0, present), scores: make([]float64, 0, present)}
	split := sort.Search(len(ids), func(i int) bool { return ids[i] >= 0 })
	appendScore := func(i int) {
		if !math.IsNaN(scores[i]) {
			t.keys = append(t.keys, scoreDocKey(ids[i]))
			t.scores = append(t.scores, scores[i])
		}
	}
	// Actual EntryID order: nonnegative ascending, then negative by magnitude.
	for i := split; i < len(ids); i++ {
		appendScore(i)
	}
	for i := split - 1; i >= 0; i-- {
		appendScore(i)
	}
	if len(t.keys) > 0 {
		t.denseKeys = t.keys[len(t.keys)-1]-t.keys[0] == uint64(len(t.keys)-1)
	}
	blocks := (len(t.keys) + scoreBlockSize - 1) / scoreBlockSize
	t.leafBase = 1
	for t.leafBase < blocks {
		t.leafBase *= 2
	}
	t.best = make([]ScoredDoc, t.leafBase*2)
	for i := range t.best {
		t.best[i] = ScoredDoc{DocID: MaxDocID, Score: math.Inf(-1)}
	}
	for i, key := range t.keys {
		leaf := t.leafBase + i/scoreBlockSize
		d := ScoredDoc{DocID: scoreKeyDocID(key), Score: t.scores[i]}
		if less(t.best[leaf], d) {
			t.best[leaf] = d
		}
	}
	for i := t.leafBase - 1; i > 0; i-- {
		t.best[i] = t.best[i*2]
		if less(t.best[i], t.best[i*2+1]) {
			t.best[i] = t.best[i*2+1]
		}
	}
	return t, nil
}

func (t *BlockMaxScoreTable) Len() int { return len(t.keys) }
func (t *BlockMaxScoreTable) Score(id DocID) (float64, bool) {
	if !ValidDocID(id) {
		return 0, false
	}
	key := scoreDocKey(id)
	i := t.lowerBound(key)
	if i == len(t.keys) || t.keys[i] != key {
		return 0, false
	}
	return t.scores[i], true
}

// Dense snapshots use direct offsets; sparse IDs retain binary search without
// adding a second document map to the sidecar.
func (t *BlockMaxScoreTable) lowerBound(key uint64) int {
	if t.denseKeys {
		if key <= t.keys[0] {
			return 0
		}
		offset := key - t.keys[0]
		if offset >= uint64(len(t.keys)) {
			return len(t.keys)
		}
		return int(offset)
	}
	return sort.Search(len(t.keys), func(i int) bool { return t.keys[i] >= key })
}

func scoreDocKey(id DocID) uint64 {
	if id < 0 {
		return 1<<43 | uint64(-id)
	}
	return uint64(id)
}
func scoreKeyDocID(key uint64) DocID {
	if key&(1<<43) != 0 {
		return -DocID(key & MaxDocID)
	}
	return DocID(key)
}

func (t *BlockMaxScoreTable) NextCompetitive(entry EntryID, threshold ScoredDoc, missing MissingScoreMode) EntryID {
	if entry.IsEnd() {
		return entry
	}
	// Without a complete index DocID universe, absent IDs cannot be enumerated.
	// Keeping the whole interval is safe, including zero-score tie winners.
	if missing != MissingScoreSkip && threshold.Score <= 0 {
		return entry
	}
	if len(t.keys) == 0 || !less(threshold, t.best[1]) {
		return EndEntryID
	}
	key := (uint64(entry) >> 4) & scoreDocKeyMask
	start := t.lowerBound(key)
	index := t.nextIndex(start, threshold)
	// EntryID = [K:8 | conjunction index:8 | sign+magnitude:44 | flags:4].
	// DocID order RESTARTS at every (K,index). A suffix bound only skips to the
	// next prefix, never across all remaining K groups. Carry must not wrap
	// the last prefix back to zero; EndEntryID is the end sentinel.
	const prefixStep uint64 = 1 << 48
	prefix := uint64(entry) &^ (prefixStep - 1)
	if index < 0 {
		if prefix == ^(prefixStep - 1) {
			return EndEntryID
		}
		prefix += prefixStep
		index = t.nextIndex(0, threshold) // root bound above guarantees a winner exists
	}
	target := EntryID(prefix | t.keys[index]<<4)
	if target < entry {
		return entry
	} // retain current include/exclude flag
	return target
}

func (t *BlockMaxScoreTable) nextIndex(start int, threshold ScoredDoc) int {
	if start >= len(t.keys) {
		return -1
	}
	block := start / scoreBlockSize
	// Inspect only the suffix of the current block, then search the max tree.
	if less(threshold, t.best[t.leafBase+block]) {
		if i := t.scanBlock(start, threshold); i >= 0 {
			return i
		}
	}
	block = t.nextBlock(1, 0, t.leafBase, block+1, threshold)
	if block < 0 {
		return -1
	}
	return t.scanBlock(block*scoreBlockSize, threshold)
}
func (t *BlockMaxScoreTable) scanBlock(start int, threshold ScoredDoc) int {
	end := min((start/scoreBlockSize+1)*scoreBlockSize, len(t.keys))
	for i := start; i < end; i++ {
		if less(threshold, ScoredDoc{DocID: scoreKeyDocID(t.keys[i]), Score: t.scores[i]}) {
			return i
		}
	}
	return -1
}

// Search left-to-right with subtree rejection; no query-time scratch allocation.
func (t *BlockMaxScoreTable) nextBlock(node, lo, hi, start int, threshold ScoredDoc) int {
	if hi <= start || !less(threshold, t.best[node]) {
		return -1
	}
	if hi-lo == 1 {
		return lo
	}
	mid := (lo + hi) / 2
	if b := t.nextBlock(node*2, lo, mid, start, threshold); b >= 0 {
		return b
	}
	return t.nextBlock(node*2+1, mid, hi, start, threshold)
}
