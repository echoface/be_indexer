package segmentindex

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	be "github.com/echoface/be_indexer"
)

// recordingLimiter captures every WaitN cost so tests can prove a merge is paced.
type recordingLimiter struct {
	mu    sync.Mutex
	calls []int
}

func (l *recordingLimiter) WaitN(_ context.Context, n int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, n)
	return nil
}

// failingLimiter fails WaitN while fail is set; tests flip the flag to model a
// merge that fails once and then recovers.
type failingLimiter struct {
	mu   sync.Mutex
	fail bool
}

func (l *failingLimiter) WaitN(_ context.Context, _ int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail {
		return errors.New("injected limiter failure")
	}
	return nil
}

func (l *failingLimiter) setFail(fail bool) {
	l.mu.Lock()
	l.fail = fail
	l.mu.Unlock()
}

func mkDoc(id be.DocID, field string, value string) *be.Document {
	return be.NewDocument(id).AddConjunction(be.NewConjunction().In(be.BEField(field), be.NewStrValues(value)))
}

func TestCompactionPreservesResults(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	// every query covers all constrained fields (city, kind, sex, age) so each
	// doc matches iff its own conjunction is satisfied
	queries := []be.Assignments{
		{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("ad"), "sex": be.NewStrValues("man"), "age": be.NewIntValues(20)},
		{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("organic"), "sex": be.NewStrValues("woman"), "age": be.NewIntValues(17)},
		{"city": be.NewStrValues("shanghai"), "kind": be.NewStrValues("organic"), "sex": be.NewStrValues("man"), "age": be.NewIntValues(30)},
		{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("ad"), "sex": be.NewStrValues("woman"), "age": be.NewIntValues(18)},
		{"city": be.NewStrValues("shanghai"), "kind": be.NewStrValues("ad"), "sex": be.NewStrValues("man"), "age": be.NewIntValues(25)},
	}
	capture := func() [][]be.DocID {
		out := make([][]be.DocID, 0, len(queries))
		for _, q := range queries {
			out = append(out, mustAssign(t, store, q))
		}
		return out
	}

	if err := store.AddDocuments(
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("ad"))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("organic"))),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDocuments(
		be.NewDocument(3).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("shanghai")).In("kind", be.NewStrValues("ad"))),
		be.NewDocument(4).AddConjunction(be.NewConjunction().In("sex", be.NewStrValues("man")).GreaterThan("age", 18)),
		be.NewDocument(5).AddConjunction(be.NewConjunction().Between("age", 0, 25)),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	// an update creates a second version of doc 3 in its own delta
	if err := store.Update(be.NewDocument(3).AddConjunction(
		be.NewConjunction().In("city", be.NewStrValues("shanghai")).In("kind", be.NewStrValues("organic")),
	)); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(2); err != nil {
		t.Fatal(err)
	}

	before := capture()
	deltasBefore := store.Stats().Deltas
	if deltasBefore != 3 {
		t.Fatalf("want 3 deltas before compaction, got %d", deltasBefore)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := capture()
	for i := range before {
		if !equalIDs(after[i], before[i]) {
			t.Fatalf("query %d changed across compaction: before %v after %v", i, before[i], after[i])
		}
	}
	// spot-check the expected content rather than trusting the before capture
	want := [][]be.DocID{
		{1, 4, 5}, // doc2 deleted; doc3's old version killed by the update
		{5},       // only doc5's [0,25) range takes age 17
		{3, 4},    // updated doc3 plus doc4's gt(18)
		{1, 5},    // gt(18) is strict at 18
		{4},       // between excludes 25; old doc3 must stay dead
	}
	for i := range want {
		if !equalIDs(after[i], want[i]) {
			t.Fatalf("query %d = %v want %v", i, after[i], want[i])
		}
	}
	if stats := store.Stats(); stats.Deltas != 1 {
		t.Fatalf("compaction left %d deltas, want 1", stats.Deltas)
	}
}

func TestCompactionCoalescesSameSignatureGroups(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	// five docs sharing one conjunction shape with disjoint ids...
	docs := []*be.Document{
		mkDoc(1, "city", "beijing"),
		mkDoc(2, "city", "beijing"),
		mkDoc(3, "city", "beijing"),
		mkDoc(4, "city", "beijing"),
		mkDoc(5, "city", "beijing"),
	}
	// ...and one doc with two same-shape conjunctions: its groups share the id
	// and must never coalesce with each other (DNF OR boundary)
	docs = append(docs, be.NewDocument(10).AddConjunction(
		be.NewConjunction().In("city", be.NewStrValues("beijing")),
		be.NewConjunction().In("city", be.NewStrValues("shanghai")),
	))
	if err := store.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Groups != 7 {
		t.Fatalf("want 7 groups before compaction, got %d", stats.Groups)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	// buckets: {docs 1..5 + one of doc10's groups}, {doc10's other group}
	stats := store.Stats()
	if stats.Groups != 2 {
		t.Fatalf("want 2 groups after compaction, got %d", stats.Groups)
	}
	if got := mustAssign(t, store, be.Assignments{"city": be.NewStrValues("beijing")}); !equalIDs(got, []be.DocID{1, 2, 3, 4, 5, 10}) {
		t.Fatalf("beijing = %v", got)
	}
	if got := mustAssign(t, store, be.Assignments{"city": be.NewStrValues("shanghai")}); !equalIDs(got, []be.DocID{10}) {
		t.Fatalf("shanghai = %v", got)
	}
	// both of doc10's conjunctions are single-field, so every assignment on
	// city is fine; the OR boundary matters for multi-field shapes
	store2 := NewStore(StoreOptions{})
	defer store2.Close()
	if err := store2.AddDocuments(be.NewDocument(1).AddConjunction(
		be.NewConjunction().In("A", be.NewIntValues(1)).In("B", be.NewIntValues(1)),
		be.NewConjunction().In("A", be.NewIntValues(2)).In("B", be.NewIntValues(2)),
	)); err != nil {
		t.Fatal(err)
	}
	if err := store2.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store2.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store2, be.Assignments{"A": be.NewIntValues(1), "B": be.NewIntValues(2)}); !equalIDs(got, nil) {
		t.Fatalf("cross-conjunction false match after compaction: %v", got)
	}
	if got := mustAssign(t, store2, be.Assignments{"A": be.NewIntValues(1), "B": be.NewIntValues(1)}); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("conjunction 1 lost: %v", got)
	}
	if got := mustAssign(t, store2, be.Assignments{"A": be.NewIntValues(2), "B": be.NewIntValues(2)}); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("conjunction 2 lost: %v", got)
	}
}

func TestCompactionTombstonePruning(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	if err := store.AddDocuments(
		mkDoc(1, "kind", "ad"),
		mkDoc(2, "kind", "ad"),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(mkDoc(1, "kind", "organic")); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(2); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Tombstones != 2 {
		t.Fatalf("want 2 tombstones before compaction, got %d", stats.Tombstones)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	// both tombstones become unreferencable: doc1's old version is physically
	// merged away and doc2 has no version left
	if stats := store.Stats(); stats.Tombstones != 0 {
		t.Fatalf("tombstones not pruned: %+v", stats)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("ad")}); !equalIDs(got, nil) {
		t.Fatalf("dead versions visible: %v", got)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("organic")}); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("live version lost: %v", got)
	}
}

func TestCompactionFailureRecovery(t *testing.T) {
	limiter := &failingLimiter{fail: true}
	store := NewStore(StoreOptions{Limiter: limiter})
	defer store.Close()
	if err := store.AddDocuments(
		mkDoc(1, "kind", "ad"),
		mkDoc(2, "kind", "ad"),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	q := be.Assignments{"kind": be.NewStrValues("ad")}
	before := mustAssign(t, store, q)

	if err := store.Compact(context.Background()); err == nil {
		t.Fatal("Compact succeeded despite failing limiter")
	}
	// the online version is untouched: same results, same deltas
	if got := mustAssign(t, store, q); !equalIDs(got, before) {
		t.Fatalf("failed compaction changed results: %v", got)
	}
	if stats := store.Stats(); stats.Deltas != 1 || stats.CompactionFailures != 1 || stats.LastCompactionError == "" {
		t.Fatalf("failure not recorded: %+v", stats)
	}

	// recovery: the next round succeeds and keeps serving the same data
	limiter.setFail(false)
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, q); !equalIDs(got, before) {
		t.Fatalf("recovered compaction changed results: %v", got)
	}
	if stats := store.Stats(); stats.Deltas != 1 || stats.Compactions != 1 {
		t.Fatalf("recovery stats = %+v", stats)
	}
}

func TestCompactionRateLimit(t *testing.T) {
	limiter := &recordingLimiter{}
	store := NewStore(StoreOptions{Limiter: limiter})
	defer store.Close()
	for id := be.DocID(1); id <= 8; id++ {
		if err := store.AddDocuments(mkDoc(id, "kind", "ad")); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.calls) == 0 {
		t.Fatal("merge ran without pacing")
	}
	total := 0
	for _, n := range limiter.calls {
		if n <= 0 {
			t.Fatalf("WaitN called with cost %d", n)
		}
		total += n
	}
	if total < 8 {
		t.Fatalf("recorded cost %d < 8 processed docs", total)
	}
}

func TestBackgroundCompaction(t *testing.T) {
	store := NewStore(StoreOptions{Compaction: CompactionOptions{
		MinDeltas:    2,
		Interval:     5 * time.Millisecond,
		MaxAttempts:  2,
		RetryBackoff: 5 * time.Millisecond,
	}})
	defer store.Close()
	for id := be.DocID(1); id <= 3; id++ {
		if err := store.AddDocuments(mkDoc(id, "kind", "ad")); err != nil {
			t.Fatal(err)
		}
		if err := store.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if stats := store.Stats(); stats.Deltas != 3 {
		t.Fatalf("want 3 deltas, got %d", stats.Deltas)
	}
	if err := store.StartBackgroundCompaction(); err != nil {
		t.Fatal(err)
	}
	if err := store.StartBackgroundCompaction(); err != nil {
		t.Fatalf("second start = %v, want nil", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		stats := store.Stats()
		if stats.Deltas == 1 && stats.Compactions >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background compaction did not converge: %+v", stats)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("ad")}); !equalIDs(got, []be.DocID{1, 2, 3}) {
		t.Fatalf("got %v want [1 2 3]", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundCompactionRequiresMinDeltas(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	if err := store.StartBackgroundCompaction(); err == nil {
		t.Fatal("expected error without MinDeltas")
	}
}

func TestTokenBucket(t *testing.T) {
	unlimited := NewTokenBucket(0, 0)
	if err := unlimited.WaitN(context.Background(), 1<<30); err != nil {
		t.Fatalf("unlimited limiter blocked: %v", err)
	}
	bucket := NewTokenBucket(1000, 1000)
	if err := bucket.WaitN(context.Background(), 10); err != nil {
		t.Fatalf("burst request failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// a request larger than the burst waits on the timer and must observe the
	// cancelled context instead of sleeping it out
	if err := NewTokenBucket(1, 1).WaitN(ctx, 1000); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
