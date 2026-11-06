package segmentindex

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
)

var (
	ErrStoreClosed       = errors.New("segmentindex: store is closed")
	ErrCompactionRunning = errors.New("segmentindex: compaction already running")
)

// CompactionOptions controls background segment merge behavior.
type CompactionOptions struct {
	// MinDeltas is the delta count that triggers a background merge.
	// Background compaction is disabled when MinDeltas <= 0.
	MinDeltas int
	// MaxDeltasPerRound limits how many deltas (the smallest first) one merge
	// round consumes; 0 merges every delta into one.
	MaxDeltasPerRound int
	// RateBytesPerSecond throttles merge throughput (doc-version cost unit
	// approximates bytes); 0 means unlimited.
	RateBytesPerSecond int64
	// RateBurst is the token bucket burst; defaults to RateBytesPerSecond.
	RateBurst int64
	// Interval is the background trigger poll period (default 250ms).
	Interval time.Duration
	// MaxAttempts retries a failed background merge (default 3).
	MaxAttempts int
	// RetryBackoff sleeps between attempts and grows linearly with the
	// attempt number (default 100ms).
	RetryBackoff time.Duration
}

// StoreOptions configures a Store.
type StoreOptions struct {
	// FlushDocs seals the pending buffer into a delta once it holds this many
	// docs; 0 keeps every batch pending until Flush is called.
	FlushDocs  int
	Compaction CompactionOptions
	// Limiter overrides the compaction rate limiter (tests inject fakes).
	Limiter RateLimiter
}

// StoreStats reports store state for monitoring.
type StoreStats struct {
	Deltas              int
	Groups              int
	PendingDocs         int
	Tombstones          int
	Version             uint64
	Compactions         int
	CompactionFailures  int
	LastCompactionError string
}

// storeState is one immutable published view: the delta list plus the
// tombstones that apply to it. Reference counting keeps a view alive while
// in-flight queries read it; publishing a new view retires the previous one.
type storeState struct {
	deltas  []*Delta
	tombs   *tombstoneSet
	version uint64

	mu      sync.Mutex
	refs    int
	retired bool
}

func (v *storeState) retain() {
	v.mu.Lock()
	v.refs++
	v.mu.Unlock()
}

// release drops one reference; the view (and its deltas) becomes collectable
// when the last reader is done.
func (v *storeState) release() {
	v.mu.Lock()
	v.refs--
	v.mu.Unlock()
}

// retire marks the view unusable for new readers and drops the publisher's
// reference.
func (v *storeState) retire() {
	v.mu.Lock()
	v.retired = true
	v.refs--
	v.mu.Unlock()
}

// StoreSnapshot is a fixed view of the store: query results stay stable while
// flushes, deletes and compactions publish newer versions.
type StoreSnapshot struct {
	state    *storeState
	released bool
}

// RetrieveAssignments evaluates full DNF assignments (including range
// predicates) against the frozen view.
func (s *StoreSnapshot) RetrieveAssignments(assignments be.Assignments) ([]be.DocID, error) {
	if s == nil || s.released {
		return nil, ErrSnapshotDone
	}
	seen := make(map[be.DocID]struct{})
	for _, delta := range s.state.deltas {
		ids, err := delta.index.retrieveAssignmentsFiltered(assignments, s.state.tombs)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	return sortedDocIDs(seen), nil
}

// Retrieve resolves an exact-term planner.Query against the frozen view.
// Range predicates are not evaluated on this path (same as Index.Retrieve).
func (s *StoreSnapshot) Retrieve(query planner.Query) ([]be.DocID, error) {
	if s == nil || s.released {
		return nil, ErrSnapshotDone
	}
	seen := make(map[be.DocID]struct{})
	for _, delta := range s.state.deltas {
		ids, err := delta.index.retrieveFiltered(query, s.state.tombs)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	return sortedDocIDs(seen), nil
}

// Close releases the snapshot reference. The store keeps working regardless.
func (s *StoreSnapshot) Close() {
	if s == nil || s.released {
		return
	}
	s.released = true
	s.state.release()
}

// Store layers delta segments, tombstones and compaction on top of the
// immutable Index building blocks: additions accumulate in a pending buffer
// and become sealed Deltas on Flush, Delete/Update record sequence-scoped
// tombstones, and Compact merges deltas into fewer segments. Every state
// change publishes a new immutable view; queries pin one view per snapshot so
// concurrent writes or merges never disturb in-flight retrievals.
type Store struct {
	mu      sync.Mutex
	opts    StoreOptions
	limiter RateLimiter

	seq       uint64
	nextDelta DeltaID

	pending     *Builder
	pendingDocs int
	pendingIDs  map[uint64]struct{}

	deltas     []*Delta
	tombs      *tombstoneSet
	view       *storeState
	closed     bool
	compacting bool

	compactions int
	failures    int
	lastErr     string

	bgCancel context.CancelFunc
	bgDone   chan struct{}
}

// NewStore returns an empty Store.
func NewStore(opts StoreOptions) *Store {
	limiter := opts.Limiter
	if limiter == nil {
		compaction := opts.Compaction
		if compaction.RateBytesPerSecond > 0 {
			limiter = NewTokenBucket(float64(compaction.RateBytesPerSecond), float64(compaction.RateBurst))
		} else {
			limiter = UnlimitedLimiter()
		}
	}
	s := &Store{
		opts:       opts,
		limiter:    limiter,
		nextDelta:  1,
		pending:    NewBuilder(),
		pendingIDs: make(map[uint64]struct{}),
		tombs:      newTombstoneSet(),
	}
	s.view = &storeState{tombs: s.tombs.clone(), version: 1, refs: 1}
	return s
}

func (s *Store) allocSeq() uint64 {
	s.seq++
	return s.seq
}

func (s *Store) allocDeltaID() DeltaID {
	id := s.nextDelta
	s.nextDelta++
	return id
}

// publishLocked installs a new immutable view and retires the previous one.
func (s *Store) publishLocked(deltas []*Delta, tombs *tombstoneSet) {
	old := s.view
	version := uint64(0)
	if old != nil {
		version = old.version
	}
	s.view = &storeState{
		deltas:  append([]*Delta(nil), deltas...),
		tombs:   tombs.clone(),
		version: version + 1,
		refs:    1,
	}
	if old != nil {
		old.retire()
	}
}

// AddDocuments appends docs to the pending buffer with fresh add sequences.
// The batch is atomic: on error nothing of it is kept. Buffered docs become
// visible only after Flush (or the FlushDocs threshold) seals a delta.
func (s *Store) AddDocuments(docs ...*be.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	return s.addLocked(docs...)
}

func (s *Store) addLocked(docs ...*be.Document) error {
	mark := len(s.pending.groups)
	added := make([]uint64, 0, len(docs))
	for _, doc := range docs {
		if doc == nil {
			s.rollbackAdd(mark, added)
			return fmt.Errorf("%w: nil document", ErrUnsupportedExpression)
		}
		seq := s.allocSeq()
		if err := s.pending.addDocument(doc, seq); err != nil {
			s.rollbackAdd(mark, added)
			return err
		}
		s.pendingDocs++
		s.pendingIDs[uint64(doc.ID)] = struct{}{}
		added = append(added, uint64(doc.ID))
	}
	if s.opts.FlushDocs > 0 && s.pendingDocs >= s.opts.FlushDocs {
		return s.flushLocked()
	}
	return nil
}

func (s *Store) rollbackAdd(mark int, added []uint64) {
	s.pending.groups = s.pending.groups[:mark]
	for _, id := range added {
		delete(s.pendingIDs, id)
	}
	s.pendingDocs -= len(added)
}

// Delete tombstones every current version of the given docs. Re-adding (or
// updating) a deleted id afterwards yields a live version again. Deleting an
// unknown id is a no-op.
func (s *Store) Delete(ids ...be.DocID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if len(ids) == 0 {
		return nil
	}
	seq := s.allocSeq()
	for _, id := range ids {
		s.tombs.kill(id, seq)
	}
	s.publishLocked(s.deltas, s.tombs)
	return nil
}

// Update replaces doc.ID: the current version is tombstoned and doc is added
// as a new version. The operation is atomic — when the add fails, the old
// version stays visible.
func (s *Store) Update(doc *be.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	if doc == nil {
		return fmt.Errorf("%w: nil document", ErrUnsupportedExpression)
	}
	tombSeq := s.allocSeq()
	addSeq := s.allocSeq()
	if err := s.pending.addDocument(doc, addSeq); err != nil {
		return err
	}
	s.pendingDocs++
	s.pendingIDs[uint64(doc.ID)] = struct{}{}
	s.tombs.kill(doc.ID, tombSeq)
	// the tombstone is visible immediately; the new version joins the pending
	// buffer and shows up after the next flush
	s.publishLocked(s.deltas, s.tombs)
	if s.opts.FlushDocs > 0 && s.pendingDocs >= s.opts.FlushDocs {
		return s.flushLocked()
	}
	return nil
}

// Flush seals the pending buffer into a new delta and publishes it. Flushing
// an empty buffer is a no-op.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	return s.flushLocked()
}

func (s *Store) flushLocked() error {
	if s.pendingDocs == 0 {
		return nil
	}
	index, err := s.pending.Seal()
	if err != nil {
		return err
	}
	delta := newDelta(s.allocDeltaID(), index)
	s.deltas = append(s.deltas, delta)
	s.pending = NewBuilder()
	s.pendingDocs = 0
	s.pendingIDs = make(map[uint64]struct{})
	s.publishLocked(s.deltas, s.tombs)
	return nil
}

// Snapshot pins the current view for stable reads across later writes.
func (s *Store) Snapshot() (*StoreSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStoreClosed
	}
	s.view.retain()
	return &StoreSnapshot{state: s.view}, nil
}

// RetrieveAssignments is a convenience one-shot full DNF retrieval.
func (s *Store) RetrieveAssignments(assignments be.Assignments) ([]be.DocID, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	return snapshot.RetrieveAssignments(assignments)
}

// Retrieve is a convenience one-shot exact-term retrieval.
func (s *Store) Retrieve(query planner.Query) ([]be.DocID, error) {
	snapshot, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	return snapshot.Retrieve(query)
}

// Compact merges deltas into one rewritten delta now: tombstoned versions are
// stripped physically, empty groups disappear, same-signature groups merge
// their postings and dead tombstones are pruned. The merge runs outside the
// store lock and publishes only on success — a failure leaves the online
// version untouched and drops every temporary artifact.
func (s *Store) Compact(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrStoreClosed
	}
	if s.compacting {
		s.mu.Unlock()
		return ErrCompactionRunning
	}
	s.compacting = true
	s.mu.Unlock()

	err := s.compactOnce(ctx)

	s.mu.Lock()
	s.compacting = false
	if err == nil {
		s.compactions++
	} else {
		s.failures++
		s.lastErr = err.Error()
	}
	s.mu.Unlock()
	return err
}

func (s *Store) compactOnce(ctx context.Context) error {
	s.mu.Lock()
	inputs := selectDeltas(s.deltas, s.opts.Compaction.MaxDeltasPerRound)
	tomb := s.tombs.clone()
	s.mu.Unlock()
	if len(inputs) == 0 {
		return nil
	}
	index, err := mergeDeltas(ctx, inputs, tomb, s.limiter)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStoreClosed
	}
	merged := newDelta(s.allocDeltaID(), index)
	remaining := replaceDeltas(s.deltas, inputs, merged)
	pruned := s.tombs.clone()
	pruned.prune(minSeqAcross(remaining), s.pendingIDs)
	s.deltas = remaining
	s.tombs = pruned
	s.publishLocked(s.deltas, s.tombs)
	return nil
}

// selectDeltas picks the merge inputs: every delta, or the n smallest ones.
func selectDeltas(deltas []*Delta, max int) []*Delta {
	if len(deltas) == 0 {
		return nil
	}
	if max <= 0 || max >= len(deltas) {
		return append([]*Delta(nil), deltas...)
	}
	sorted := append([]*Delta(nil), deltas...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].docs != sorted[j].docs {
			return sorted[i].docs < sorted[j].docs
		}
		return sorted[i].id < sorted[j].id
	})
	return sorted[:max]
}

// replaceDeltas swaps the consumed inputs for the merged delta, keeping the
// relative order of untouched deltas. Inputs not found (already replaced by a
// concurrent view change) are simply dropped.
func replaceDeltas(deltas []*Delta, inputs []*Delta, merged *Delta) []*Delta {
	consumed := make(map[DeltaID]struct{}, len(inputs))
	for _, delta := range inputs {
		consumed[delta.id] = struct{}{}
	}
	out := make([]*Delta, 0, len(deltas)-len(inputs)+1)
	inserted := false
	for _, delta := range deltas {
		if _, gone := consumed[delta.id]; gone {
			if !inserted {
				out = append(out, merged)
				inserted = true
			}
			continue
		}
		out = append(out, delta)
	}
	if !inserted {
		out = append(out, merged)
	}
	return out
}

// minSeqAcross maps each doc id to its smallest remaining add sequence.
func minSeqAcross(deltas []*Delta) map[uint64]uint64 {
	minSeq := make(map[uint64]uint64)
	for _, delta := range deltas {
		for _, meta := range delta.index.groups {
			for id, seq := range meta.seqs {
				if current, ok := minSeq[id]; !ok || seq < current {
					minSeq[id] = seq
				}
			}
		}
	}
	return minSeq
}

// StartBackgroundCompaction launches the merge worker: when the delta count
// reaches MinDeltas it runs one merge round, paced by the configured rate
// limiter and retried with backoff on failure. The worker touches only
// published state and can be stopped with Close.
func (s *Store) StartBackgroundCompaction() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrStoreClosed
	}
	if s.bgCancel != nil {
		s.mu.Unlock()
		return nil // already running
	}
	if s.opts.Compaction.MinDeltas <= 0 {
		s.mu.Unlock()
		return fmt.Errorf("segmentindex: Compaction.MinDeltas must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.bgCancel = cancel
	s.bgDone = done
	s.mu.Unlock()
	go s.backgroundLoop(ctx, done)
	return nil
}

func (s *Store) backgroundLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	compaction := s.opts.Compaction
	interval := compaction.Interval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	maxAttempts := compaction.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	backoff := compaction.RetryBackoff
	if backoff <= 0 {
		backoff = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		ready := !s.closed && len(s.deltas) >= compaction.MinDeltas
		s.mu.Unlock()
		if !ready {
			continue
		}
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			if err := s.Compact(ctx); err == nil || errors.Is(err, ErrCompactionRunning) || errors.Is(err, ErrStoreClosed) {
				break
			} else if attempt < maxAttempts {
				wait := time.Duration(attempt) * backoff
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}
}

// Stats reports store state for monitoring.
func (s *Store) Stats() StoreStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := StoreStats{
		Deltas:              len(s.deltas),
		PendingDocs:         s.pendingDocs,
		Tombstones:          s.tombs.len(),
		Version:             s.view.version,
		Compactions:         s.compactions,
		CompactionFailures:  s.failures,
		LastCompactionError: s.lastErr,
	}
	for _, delta := range s.deltas {
		stats.Groups += delta.groups
	}
	return stats
}

// Close stops background compaction and retires the current view. In-flight
// snapshots stay valid until closed by their owners; all further store
// operations fail with ErrStoreClosed.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel, done := s.bgCancel, s.bgDone
	s.bgCancel = nil
	if s.view != nil {
		s.view.retire()
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

func sortedDocIDs(seen map[be.DocID]struct{}) []be.DocID {
	result := make([]be.DocID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(a, b int) bool { return result[a] < result[b] })
	return result
}
