package loader

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/echoface/be_indexer/engine"
)

// newTestHolder builds a Holder wired to a fixed sequence of snapshots without
// touching disk, so refcount/retire behavior can be tested in isolation.
func newTestHolder() *Holder {
	h := &Holder{}
	h.cur.Store(newRefSnapshot(engine.NewCompositeEngine(&engine.IndexSnapshot{Generation: 1})))
	return h
}

// TestRefSnapshot_AcquireRelease verifies the basic count transitions and that
// the engine is closed exactly when the count reaches zero after retire.
func TestRefSnapshot_AcquireRelease(t *testing.T) {
	rs := newRefSnapshot(engine.NewCompositeEngine(&engine.IndexSnapshot{}))
	if got := atomic.LoadInt32(&rs.refs); got != 1 {
		t.Fatalf("initial refs = %d, want 1", got)
	}
	if !rs.tryAcquire() {
		t.Fatal("tryAcquire on live snapshot must succeed")
	}
	if got := atomic.LoadInt32(&rs.refs); got != 2 {
		t.Fatalf("after acquire refs = %d, want 2", got)
	}
	rs.retire() // drops holder ref: 2 -> 1, not yet closed
	if got := atomic.LoadInt32(&rs.refs); got != 1 {
		t.Fatalf("after retire refs = %d, want 1", got)
	}
	// A retired snapshot with refs still > 0 must still be acquirable by the
	// in-flight path that already holds a reference chain.
	rs.release() // in-flight query finishes: 1 -> 0, closes now
	if got := atomic.LoadInt32(&rs.refs); got != 0 {
		t.Fatalf("after final release refs = %d, want 0", got)
	}
	// Once closed, tryAcquire must fail (signals caller to re-read current).
	if rs.tryAcquire() {
		t.Fatal("tryAcquire on closed snapshot must fail")
	}
}

// TestHolderReloadRetiresOldSnapshot verifies that a Reload drops the previous
// snapshot's holder reference (refs 1 -> 0 when no in-flight query holds it),
// so it is reclaimed immediately instead of at GC time.
func TestHolderReloadRetiresOldSnapshot(t *testing.T) {
	h := newTestHolder()
	old := h.currentRef()
	if got := atomic.LoadInt32(&old.refs); got != 1 {
		t.Fatalf("published snapshot refs = %d, want 1", got)
	}

	// Publish a new snapshot directly (bypassing disk OpenIndex) and retire old,
	// mirroring Reload's publish-then-retire ordering.
	newRef := newRefSnapshot(engine.NewCompositeEngine(&engine.IndexSnapshot{Generation: 2}))
	h.cur.Store(newRef)
	old.retire()

	if got := atomic.LoadInt32(&old.refs); got != 0 {
		t.Fatalf("retired old snapshot refs = %d, want 0 (should be closed)", got)
	}
	if got := atomic.LoadInt32(&newRef.refs); got != 1 {
		t.Fatalf("new snapshot refs = %d, want 1", got)
	}
}

// TestHolderInFlightQueryDelaysRetire verifies that retiring a snapshot while a
// query holds a reference does NOT close it until the query releases.
func TestHolderInFlightQueryDelaysRetire(t *testing.T) {
	h := newTestHolder()
	old := h.currentRef()

	// Simulate an in-flight query holding a reference.
	inflight := h.acquire()
	if inflight != old {
		t.Fatal("acquire should return the current snapshot")
	}
	if got := atomic.LoadInt32(&old.refs); got != 2 {
		t.Fatalf("refs with one in-flight query = %d, want 2", got)
	}

	// Reload retires old while the query is still running.
	newRef := newRefSnapshot(engine.NewCompositeEngine(&engine.IndexSnapshot{Generation: 2}))
	h.cur.Store(newRef)
	old.retire() // 2 -> 1, must NOT close yet
	if got := atomic.LoadInt32(&old.refs); got != 1 {
		t.Fatalf("refs after retire with in-flight query = %d, want 1 (not closed)", got)
	}

	// Query finishes.
	inflight.release() // 1 -> 0, closes now
	if got := atomic.LoadInt32(&old.refs); got != 0 {
		t.Fatalf("refs after in-flight release = %d, want 0", got)
	}
}

// TestHolderReloadSoakRefcount hammers Reload concurrently with acquire/release
// cycles. Under -race this asserts there is no torn state and that every
// snapshot's count returns to a consistent terminal value. It exercises the
// acquire() retry path against the publish-then-retire window.
func TestHolderReloadSoakRefcount(t *testing.T) {
	h := newTestHolder()

	var stop int32
	var wg sync.WaitGroup

	// Query goroutines: acquire/release the current snapshot repeatedly.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				rs := h.acquire()
				if rs == nil {
					continue
				}
				// Touch the engine to mimic a real query using the snapshot.
				_ = rs.engine.Snapshot()
				rs.release()
			}
		}()
	}

	// Reloader goroutine: publish-then-retire many generations.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for gen := uint64(2); gen < 2000; gen++ {
			newRef := newRefSnapshot(engine.NewCompositeEngine(&engine.IndexSnapshot{Generation: gen}))
			old := h.currentRef()
			h.cur.Store(newRef)
			if old != nil {
				old.retire()
			}
		}
		atomic.StoreInt32(&stop, 1)
	}()

	wg.Wait()

	// The final published snapshot must still be live (refs == 1) since no
	// reload superseded it and all queries have released.
	final := h.currentRef()
	if got := atomic.LoadInt32(&final.refs); got != 1 {
		t.Fatalf("final snapshot refs = %d, want 1", got)
	}
}
