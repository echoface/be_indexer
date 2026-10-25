package core

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// ScoreSource maps a DocID to its ranking score. Implementations must be
// immutable once published: a retrieval snapshots the source once and relies
// on it returning consistent scores for the whole query.
type ScoreSource interface {
	// Score returns the score for id and whether id has a score at all.
	Score(id DocID) (float64, bool)
}

// ScoredDoc pairs a matched DocID with its ranking score.
type ScoredDoc struct {
	DocID DocID
	Score float64
}

// AtomicScoreSource is a concurrency-safe holder for an immutable
// ScoreSource. Producers build a complete new source and Store it; each
// retrieval Load()s once and uses that snapshot for its entire lifetime.
type AtomicScoreSource struct {
	ptr atomic.Pointer[scoreSourceBox]
}

type scoreSourceBox struct{ src ScoreSource }

func (s *AtomicScoreSource) Store(src ScoreSource) {
	s.ptr.Store(&scoreSourceBox{src: src})
}

// Snapshot returns the currently published source, or nil when none has been
// stored yet.
func (s *AtomicScoreSource) Snapshot() ScoreSource {
	p := s.ptr.Load()
	if p == nil {
		return nil
	}
	return p.src
}

// DenseScoreTable stores scores for a contiguous DocID range as
// scores[id-base]. It is the fastest source (one bounds check plus one array
// access) but only suitable when IDs occupy their range densely.
type DenseScoreTable struct {
	base   DocID
	scores []float64
}

// NewDenseScoreTable builds a table over the contiguous range
// [base, base+len(scores)). Slots holding NaN are treated as absent.
func NewDenseScoreTable(base DocID, scores []float64) *DenseScoreTable {
	return &DenseScoreTable{base: base, scores: scores}
}

func (t *DenseScoreTable) Score(id DocID) (float64, bool) {
	if id < t.base || id >= t.base+DocID(len(t.scores)) {
		return 0, false
	}
	s := t.scores[id-t.base]
	return s, !math.IsNaN(s)
}

// RoaringScoreTable stores sparse (DocID -> score) pairs using a roaring
// bitmap for membership plus a parallel array of scores ordered by DocID.
// Rank gives the array index directly, so lookups touch one or two cache
// lines instead of the ~23 random probes of a binary search over tens of
// millions of entries.
type RoaringScoreTable struct {
	ids    *roaring64.Bitmap
	scores []float64
}

// NewRoaringScoreTable builds a table from parallel id/score slices. The
// slices must be sorted by ascending id; duplicate IDs are rejected because
// they almost always indicate a producer bug rather than an intentional
// overwrite.
func NewRoaringScoreTable(ids []DocID, scores []float64) (*RoaringScoreTable, error) {
	if len(ids) != len(scores) {
		return nil, fmt.Errorf("score table: ids (%d) and scores (%d) length mismatch", len(ids), len(scores))
	}
	bm := roaring64.New()
	for i, id := range ids {
		if i > 0 && id <= ids[i-1] {
			return nil, fmt.Errorf("score table: ids must be strictly ascending, got %d after %d", id, ids[i-1])
		}
		bm.Add(uint64(id))
	}
	// scores must be reordered into bitmap iteration (ascending id) order.
	ordered := append([]float64(nil), scores...)
	return &RoaringScoreTable{ids: bm, scores: ordered}, nil
}

func (t *RoaringScoreTable) Score(id DocID) (float64, bool) {
	u := uint64(id)
	if !t.ids.Contains(u) {
		return 0, false
	}
	return t.scores[t.ids.Rank(u)-1], true
}

func (t *RoaringScoreTable) Len() int { return len(t.scores) }
