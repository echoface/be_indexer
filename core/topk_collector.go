package core

import (
	"container/heap"
	"sort"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// MissingScoreMode controls how TopKCollector treats a matched DocID that has
// no entry in the attached score source.
type MissingScoreMode uint8

const (
	// MissingScoreZero scores absent documents as 0 and still considers them
	// candidates. This keeps the result size stable when the index and the
	// score table are briefly out of sync.
	MissingScoreZero MissingScoreMode = iota
	// MissingScoreSkip drops documents without a score. They neither enter
	// the result nor consume TopK quota.
	MissingScoreSkip
)

// TopKCollector keeps at most k unique DocIDs. Without a score source every
// document ranks equally and collection stops after k unique hits. With a
// score source it maintains a min-heap of size k: once full, only a document
// scoring higher than the current threshold can displace the weakest entry.
type TopKCollector struct {
	k       int
	scores  ScoreSource
	missing MissingScoreMode

	seen      *roaring64.Bitmap
	scoreHeap scoreHeap // min-heap by (Score, DocID); capacity k when scored
	count     int       // unique docs counted toward k (unscored mode only)
}

func NewTopKCollector(k int, scores ScoreSource) *TopKCollector {
	if k < 0 {
		k = 0
	}
	return &TopKCollector{
		k:       k,
		scores:  scores,
		missing: MissingScoreZero,
		seen:    roaring64.New(),
	}
}

// SetMissingScoreMode selects the absent-score policy. Must be called before
// retrieval starts.
func (c *TopKCollector) SetMissingScoreMode(m MissingScoreMode) { c.missing = m }

// Add implements ResultCollector. Duplicate DocIDs (the engine can emit one
// document through several matching conjunctions) never count twice.
func (c *TopKCollector) Add(id DocID) CollectResult {
	if c.k == 0 {
		return CollectStop
	}
	if !c.seen.CheckedAdd(uint64(id)) {
		return c.result()
	}

	if c.scores == nil {
		c.count++
	} else {
		score, ok := c.scores.Score(id)
		if !ok {
			switch c.missing {
			case MissingScoreSkip:
				return c.result()
			default:
				score = 0
			}
		}
		if len(c.scoreHeap.docs) < c.k {
			heap.Push(&c.scoreHeap, ScoredDoc{DocID: id, Score: score})
		} else if less(c.scoreHeap.docs[0], ScoredDoc{DocID: id, Score: score}) {
			c.scoreHeap.docs[0] = ScoredDoc{DocID: id, Score: score}
			heap.Fix(&c.scoreHeap, 0)
		}
	}
	return c.result()
}

func (c *TopKCollector) result() CollectResult {
	if c.k == 0 {
		return CollectStop
	}
	if c.scores == nil {
		return boolResult(c.count >= c.k)
	}
	return boolResult(len(c.scoreHeap.docs) >= c.k)
}

func boolResult(b bool) CollectResult {
	if b {
		return CollectStop
	}
	return CollectContinue
}

// Scored returns the collected documents in descending score order; ties are
// broken by ascending DocID for deterministic output.
func (c *TopKCollector) Scored() []ScoredDoc {
	if c.scores == nil {
		out := make([]ScoredDoc, 0, c.count)
		it := c.seen.Iterator()
		for it.HasNext() {
			out = append(out, ScoredDoc{DocID: DocID(it.Next()), Score: 0})
		}
		return out
	}
	out := append([]ScoredDoc(nil), c.scoreHeap.docs...)
	sortScoredDesc(out)
	return out
}

// DocIDs returns collected DocIDs. In scored mode they are ordered by
// descending score (tie: ascending DocID); otherwise by insertion order.
func (c *TopKCollector) DocIDs() []DocID {
	if c.scores == nil {
		out := make([]DocID, 0, c.count)
		it := c.seen.Iterator()
		for it.HasNext() {
			out = append(out, DocID(it.Next()))
		}
		return out
	}
	scored := c.Scored()
	out := make([]DocID, 0, len(scored))
	for _, d := range scored {
		out = append(out, d.DocID)
	}
	return out
}

// Len reports how many unique documents are currently collected.
func (c *TopKCollector) Count() int {
	if c.scores == nil {
		return c.count
	}
	return int(c.seen.GetCardinality())
}

// scoreHeap adapts the collector's internal slice to heap.Interface. Keeping
// these methods on a separate (unexported) type avoids clashing with the
// public Len() API and keeps the heap invariant private.
type scoreHeap struct {
	docs []ScoredDoc
}

func (h *scoreHeap) Len() int           { return len(h.docs) }
func (h *scoreHeap) Less(i, j int) bool { return less(h.docs[i], h.docs[j]) }
func (h *scoreHeap) Swap(i, j int)      { h.docs[i], h.docs[j] = h.docs[j], h.docs[i] }
func (h *scoreHeap) Push(x any)         { h.docs = append(h.docs, x.(ScoredDoc)) }
func (h *scoreHeap) Pop() any {
	old := h.docs
	n := len(old)
	x := old[n-1]
	h.docs = old[:n-1]
	return x
}

func less(a, b ScoredDoc) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.DocID < b.DocID
}

func sortScoredDesc(docs []ScoredDoc) {
	sort.Slice(docs, func(i, j int) bool { return less(docs[j], docs[i]) })
}
