package loader

import (
	"sync"
	"sync/atomic"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
)

// Holder owns the current immutable index snapshot and supports atomic reloads.
//
// Reload builds a complete new snapshot first and publishes it only after all
// manifest, checksum, sidecar and segment validation succeeds. Existing queries
// continue to use the previous snapshot if reload fails.
type Holder struct {
	root   string
	fields map[core.BEField]*core.FieldMeta
	opts   Options
	mu     sync.Mutex
	cur    atomic.Value // *engine.CompositeEngine
}

// NewHolder loads the initial index and returns a reloadable holder.
func NewHolder(root string, fields map[core.BEField]*core.FieldMeta, opts Options) (*Holder, error) {
	h := &Holder{root: root, fields: fields, opts: opts}
	if err := h.Reload(); err != nil {
		return nil, err
	}
	return h, nil
}

// Current returns the currently published engine, or nil before a successful load.
func (h *Holder) Current() *engine.CompositeEngine {
	if h == nil {
		return nil
	}
	v := h.cur.Load()
	if v == nil {
		return nil
	}
	return v.(*engine.CompositeEngine)
}

// Reload atomically publishes a newly loaded engine.
func (h *Holder) Reload() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	ce, err := OpenIndex(h.root, h.fields, h.opts)
	if err != nil {
		return err
	}
	h.cur.Store(ce)
	return nil
}

// Retrieve executes a query against the current engine.
func (h *Holder) Retrieve(queries core.Assignments, opts ...core.IndexOpt) (core.DocIDList, error) {
	cur := h.Current()
	if cur == nil {
		return nil, nil
	}
	return cur.Retrieve(queries, opts...)
}

// RetrieveWithCollector executes a query against the current engine.
func (h *Holder) RetrieveWithCollector(queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt) error {
	cur := h.Current()
	if cur == nil {
		return nil
	}
	return cur.RetrieveWithCollector(queries, collector, opts...)
}

// Close releases the currently published engine and its segment readers (e.g.
// unmaps mmap'd files). It is intended for full shutdown; do not call it while
// queries may still be running. Note Reload deliberately does not eagerly close
// the superseded snapshot: in-flight queries may still hold its zero-copy views,
// so its mmap is reclaimed by the segment reader finalizer once unreferenced.
func (h *Holder) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.Current()
	if cur == nil {
		return nil
	}
	return cur.Close()
}
