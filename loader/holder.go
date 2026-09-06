package loader

import (
	"sync"
	"sync/atomic"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
)

// refSnapshot wraps a published CompositeEngine with a reference count so a
// superseded snapshot is unmapped exactly when the last in-flight query
// releases it — instead of waiting for the GC finalizer.
//
// Lifecycle:
//   - Published with refs == 1 (the holder's own "current" reference).
//   - Each query does tryAcquire()/release() around its use.
//   - Reload retire()s the old snapshot, dropping the holder reference. When the
//     count reaches zero (no in-flight query left), the underlying engine — and
//     thus its mmap'd segments — is closed immediately.
type refSnapshot struct {
	engine *engine.CompositeEngine
	refs   int32 // atomic; number of live references (holder + in-flight queries)
}

func newRefSnapshot(e *engine.CompositeEngine) *refSnapshot {
	return &refSnapshot{engine: e, refs: 1}
}

// tryAcquire increments the reference count, but only while it is still
// positive. It fails once the count has dropped to zero (the snapshot is being
// or has been closed), signaling the caller to re-read the current snapshot.
func (rs *refSnapshot) tryAcquire() bool {
	for {
		n := atomic.LoadInt32(&rs.refs)
		if n <= 0 {
			return false
		}
		if atomic.CompareAndSwapInt32(&rs.refs, n, n+1) {
			return true
		}
	}
}

// release drops one reference. The engine is closed when the count reaches zero,
// which can only happen after retire() has dropped the holder's own reference.
func (rs *refSnapshot) release() {
	if atomic.AddInt32(&rs.refs, -1) == 0 {
		_ = rs.engine.Close()
	}
}

// retire drops the holder's own reference. If no query is currently holding the
// snapshot it closes immediately; otherwise the last in-flight release() closes
// it. Called exactly once per snapshot when it is superseded or on shutdown.
func (rs *refSnapshot) retire() {
	rs.release()
}

// Holder owns the current immutable index snapshot and supports atomic reloads.
//
// Reload builds a complete new snapshot first and publishes it only after all
// manifest, checksum, sidecar and segment validation succeeds. Existing queries
// continue to use the previous snapshot if reload fails.
//
// Superseded snapshots are reclaimed via reference counting: a reload retires
// the old snapshot, and its mmap'd segments are unmapped as soon as the last
// in-flight query finishes — bounding mmap/fd/page-table accumulation under
// high-frequency reload. The SegmentReader GC finalizer remains as a safety net
// only, not the primary reclamation path.
type Holder struct {
	root               string
	fields             core.Schema
	opts               Options
	mu                 sync.Mutex
	currentManifestRef string       // guarded by mu; manifests are immutable by reference
	cur                atomic.Value // *refSnapshot
}

// NewHolder loads the initial index and returns a reloadable holder.
func NewHolder(root string, fields core.Schema, opts Options) (*Holder, error) {
	h := &Holder{root: root, fields: fields, opts: opts}
	if err := h.Reload(); err != nil {
		return nil, err
	}
	return h, nil
}

// currentRef returns the published *refSnapshot, or nil before a successful load.
func (h *Holder) currentRef() *refSnapshot {
	v := h.cur.Load()
	if v == nil {
		return nil
	}
	rs, _ := v.(*refSnapshot)
	return rs
}

// acquire loads the current snapshot and takes a reference on it. It retries on
// the rare race where the snapshot is retired between load and increment;
// because Reload publishes the new snapshot before retiring the old one, a retry
// always observes a live snapshot and terminates quickly. Returns nil before the
// first successful load.
func (h *Holder) acquire() *refSnapshot {
	for {
		rs := h.currentRef()
		if rs == nil {
			return nil
		}
		if rs.tryAcquire() {
			return rs
		}
	}
}

// UnsafeCurrent returns the currently published engine, or nil before a successful
// load.
//
// Caution: the returned engine is only safe to use immediately. It is NOT
// reference-counted for the caller, so a subsequent Reload may retire and close
// it while a long-lived caller still holds it. Use Retrieve/RetrieveWithCollector
// for query traffic (they take an internal reference for the duration of the
// call); treat UnsafeCurrent as diagnostic-only and never hold it across a Reload.
func (h *Holder) UnsafeCurrent() *engine.CompositeEngine {
	if h == nil {
		return nil
	}
	rs := h.currentRef()
	if rs == nil {
		return nil
	}
	return rs.engine
}

// Reload atomically publishes a newly loaded engine and retires the previous
// snapshot. If CURRENT still contains the already-loaded immutable manifest
// reference, Reload is a no-op and does not reopen any segment.
func (h *Holder) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	manifestRef, err := ReadCurrentManifestRef(h.root)
	if err != nil {
		return err
	}
	if manifestRef == h.currentManifestRef && h.currentRef() != nil {
		return nil
	}

	ce, err := openIndexAt(h.root, manifestRef, h.fields, h.opts)
	if err != nil {
		return err
	}
	newRef := newRefSnapshot(ce)

	var old *refSnapshot
	if prev := h.cur.Load(); prev != nil {
		old, _ = prev.(*refSnapshot)
	}
	// Publish first, then retire the old snapshot so any acquire() racing with
	// this store observes a live snapshot to retry against.
	h.cur.Store(newRef)
	h.currentManifestRef = manifestRef
	if old != nil {
		old.retire()
	}
	return nil
}

// Retrieve executes a query against the current engine, holding a reference for
// the duration so a concurrent Reload cannot unmap the snapshot mid-query.
func (h *Holder) Retrieve(queries core.Assignments, opts ...core.IndexOpt) (*core.BitmapDocSet, error) {
	rs := h.acquire()
	if rs == nil {
		return core.NewBitmapDocSet(), nil
	}
	defer rs.release()
	return rs.engine.Retrieve(queries, opts...)
}

// RetrieveWithCollector executes a query against the current engine, holding a
// reference for the duration of the call.
func (h *Holder) RetrieveWithCollector(queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt) error {
	if collector == nil {
		return core.ErrNilResultCollector
	}
	rs := h.acquire()
	if rs == nil {
		return nil
	}
	defer rs.release()
	return rs.engine.RetrieveWithCollector(queries, collector, opts...)
}

// Close releases the currently published engine and its segment readers (e.g.
// unmaps mmap'd files). It retires the current snapshot; if in-flight queries
// still hold it, the actual unmap happens when the last one releases. It is
// intended for full shutdown.
func (h *Holder) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	prev := h.cur.Load()
	if prev == nil {
		return nil
	}
	rs, _ := prev.(*refSnapshot)
	if rs == nil {
		return nil
	}
	// Drop the published reference so future acquire() sees nothing and the
	// snapshot is closed once outstanding queries drain.
	h.cur.Store((*refSnapshot)(nil))
	h.currentManifestRef = ""
	rs.retire()
	return nil
}
