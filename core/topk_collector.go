package core

import (
	"math"
	"sort"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// MissingScoreMode controls how TopKCollector treats absent (including NaN) scores.
type MissingScoreMode uint8

const (
	// MissingScoreZero considers absent documents with score zero.
	MissingScoreZero MissingScoreMode = iota
	// MissingScoreSkip excludes absent documents without consuming quota.
	MissingScoreSkip
)

// CompetitiveCollector may skip an EntryID interval before Boolean evaluation.
// NextCandidate must return a target >= entry and certify that no document in
// [entry, target) can improve the result. EndEntryID certifies completion.
type CompetitiveCollector interface {
	ResultCollector
	NextCandidate(entry EntryID) EntryID
}

// TopKCollector keeps k unique documents. A scored collector traverses until
// exhaustion or a proven score bound; filling its heap alone NEVER permits
// early termination. A nil source retains the legacy first-k-hit behavior.
// Scored results are appended until k distinct eligible documents arrive;
// only then is the array heapified, once, in O(k) time.
// Reuse with Reset for allocation-free scored collection after construction.
// A collector belongs to one retrieval goroutine, not to a shared engine.
type TopKCollector struct {
	k       int
	scores  ScoreSource
	bounds  CompetitiveScoreSource
	missing MissingScoreMode
	seen    *roaring64.Bitmap // legacy unscored first-k mode only
	docs    []ScoredDoc       // append-only until full; then worst rank at the root
	members docIDSet          // only retained winners; evicted IDs need not stay in memory
	count   int
}

func NewTopKCollector(k int, scores ScoreSource) *TopKCollector {
	if k < 0 {
		k = 0
	}
	c := &TopKCollector{k: k, seen: roaring64.New(), docs: make([]ScoredDoc, 0, k)}
	c.members.init(k)
	c.Reset(scores)
	return c
}

// AcceptsDuplicates avoids a second, unbounded seen set in front of the heap.
// Immutable scores and a monotonically improving threshold ensure evicted
// duplicates cannot re-enter the result or consume another slot.
func (*TopKCollector) AcceptsDuplicates() bool { return true }

// Reset preserves capacity, k and the missing-score policy. The new source must
// be immutable for the entire query. Take AtomicScoreSource.Snapshot once here.
func (c *TopKCollector) Reset(scores ScoreSource) {
	c.scores = scores
	c.bounds, _ = scores.(CompetitiveScoreSource)
	c.docs = c.docs[:0]
	c.members.clear()
	c.seen.Clear()
	c.count = 0
}

func (c *TopKCollector) SetMissingScoreMode(m MissingScoreMode) { c.missing = m }

func (c *TopKCollector) Add(id DocID) CollectResult {
	if c.k == 0 {
		return CollectStop
	}
	if c.scores == nil {
		if c.count >= c.k {
			return CollectStop
		}
		if c.seen.CheckedAdd(uint64(id)) {
			c.count++
		}
		if c.count >= c.k {
			return CollectStop
		}
		return CollectContinue
	}
	if c.members.contains(id) {
		return CollectContinue
	}
	score, ok := c.scores.Score(id)
	if !ok || math.IsNaN(score) {
		if c.missing == MissingScoreSkip {
			return CollectContinue
		}
		score = 0
	}
	doc := ScoredDoc{DocID: id, Score: score}
	if len(c.docs) < c.k {
		c.members.add(id)
		c.docs = append(c.docs, doc)
		if len(c.docs) == c.k {
			// No threshold is valid before k unique eligible results exist.
			// Heapify only at this transition, bottom-up so each child's
			// subtree is already a heap. Reset returns us to the append phase.
			for i := len(c.docs)/2 - 1; i >= 0; i-- {
				c.siftDown(i)
			}
		}
	} else if less(c.docs[0], doc) {
		c.members.remove(c.docs[0].DocID)
		c.members.add(id)
		c.docs[0] = doc
		c.siftDown(0)
	}
	return CollectContinue
}

// siftDown restores heap order below i, assuming its child subtrees are heaps.
// Typed operations avoid container/heap's per-hit interface boxing, including
// the first full-heap transition and subsequent winner replacements.
func (c *TopKCollector) siftDown(i int) {
	for {
		child := 2*i + 1
		if child >= len(c.docs) {
			return
		}
		if child+1 < len(c.docs) && less(c.docs[child+1], c.docs[child]) {
			child++
		}
		if !less(c.docs[child], c.docs[i]) {
			return
		}
		c.docs[i], c.docs[child] = c.docs[child], c.docs[i]
		i = child
	}
}

// Threshold returns the worst retained winner only after the scored heap fills.
func (c *TopKCollector) Threshold() (ScoredDoc, bool) {
	if c.scores == nil || c.k == 0 || len(c.docs) < c.k {
		return ScoredDoc{}, false
	}
	return c.docs[0], true
}

func (c *TopKCollector) NextCandidate(entry EntryID) EntryID {
	if c.k == 0 || (c.scores == nil && c.count >= c.k) {
		return EndEntryID
	}
	threshold, full := c.Threshold()
	if !full || entry.IsEnd() {
		return entry
	}
	if c.bounds != nil {
		return c.bounds.NextCompetitive(entry, threshold, c.missing)
	}
	id := entry.ConjID().DocID()
	score, ok := c.scores.Score(id)
	if !ok || math.IsNaN(score) {
		if c.missing == MissingScoreSkip {
			return NewEntryID(entry.ConjID(), true) + 1
		}
		score = 0
	}
	if c.members.contains(id) || !less(threshold, ScoredDoc{DocID: id, Score: score}) {
		// Skip this Conjunction only: DocID order restarts at each (K,index).
		return NewEntryID(entry.ConjID(), true) + 1
	}
	return entry
}

// Scored returns an owned result ordered by descending score, then ascending
// signed DocID. The unscored legacy mode sorts its first k hits by signed ID.
func (c *TopKCollector) Scored() []ScoredDoc {
	out := append([]ScoredDoc(nil), c.docs...)
	if c.scores == nil {
		out = make([]ScoredDoc, 0, c.count)
		it := c.seen.Iterator()
		for it.HasNext() {
			out = append(out, ScoredDoc{DocID: DocID(it.Next())})
		}
	}
	sortScoredDesc(out)
	return out
}

func (c *TopKCollector) DocIDs() []DocID {
	scored := c.Scored()
	out := make([]DocID, len(scored))
	for i, d := range scored {
		out[i] = d.DocID
	}
	return out
}

// Count reports the number of retained unique documents (at most k).
func (c *TopKCollector) Count() int {
	if c.scores == nil {
		return c.count
	}
	return len(c.docs)
}

// less means worse rank: the heap root has lowest score / largest signed ID.
func less(a, b ScoredDoc) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	return a.DocID > b.DocID
}
func sortScoredDesc(docs []ScoredDoc) {
	sort.Slice(docs, func(i, j int) bool { return less(docs[j], docs[i]) })
}

// docIDSet is a fixed-capacity open-addressed set. Backshift deletion avoids
// tombstone accumulation and Go map growth allocations during heap replacement.
// At most k slots are occupied in a power-of-two table with >= 2*k slots.
type docIDSlot struct {
	id   DocID
	used bool
}
type docIDSet []docIDSlot

func (s *docIDSet) init(k int) {
	if k == 0 {
		return
	}
	n := 2
	for n/2 < k {
		n *= 2
	}
	*s = make(docIDSet, n)
}
func (s docIDSet) clear() { clear(s) }
func (s docIDSet) home(id DocID) int {
	x := uint64(id)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return int(x & uint64(len(s)-1))
}
func (s docIDSet) contains(id DocID) bool {
	if len(s) == 0 {
		return false
	}
	for i := s.home(id); s[i].used; i = (i + 1) & (len(s) - 1) {
		if s[i].id == id {
			return true
		}
	}
	return false
}
func (s docIDSet) add(id DocID) {
	i := s.home(id)
	for s[i].used {
		i = (i + 1) & (len(s) - 1)
	}
	s[i] = docIDSlot{id: id, used: true}
}
func (s docIDSet) remove(id DocID) {
	mask := len(s) - 1
	i := s.home(id)
	for s[i].id != id || !s[i].used {
		i = (i + 1) & mask
	}
	for j := (i + 1) & mask; s[j].used; j = (j + 1) & mask {
		home := s.home(s[j].id)
		if ((j - home) & mask) >= ((j - i) & mask) {
			s[i] = s[j]
			i = j
		}
	}
	s[i] = docIDSlot{}
}
