package be_indexer

import (
	"errors"
	"fmt"
	"sync"
)

var (
	ErrSnapshotManagerClosed = errors.New("snapshot manager is closed")
	ErrNoCurrentSnapshot     = errors.New("no current snapshot")
	ErrSnapshotClosed        = errors.New("snapshot is closed")
	ErrBuildClosed           = errors.New("snapshot build is closed")
)

type snapshotState struct {
	mu sync.Mutex

	index   BEIndex
	version uint64
	refs    int
	retired bool
}

func (s *snapshotState) retain() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index == nil {
		return false
	}
	s.refs++
	return true
}

func (s *snapshotState) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs > 0 {
		s.refs--
	}
	if s.refs == 0 {
		// Releasing the last manager/caller reference makes the immutable index
		// eligible for collection while preserving the version metadata.
		s.index = nil
	}
}

func (s *snapshotState) retire() {
	s.mu.Lock()
	s.retired = true
	s.mu.Unlock()
	// The manager owns one reference for the current snapshot.
	s.release()
}

// Snapshot is a caller-owned handle to an immutable, point-in-time index view.
// Each handle has an independent Close lifecycle. A handle remains queryable
// after publication of a newer version until that handle is closed.
type Snapshot struct {
	mu sync.Mutex

	state    *snapshotState
	released bool
}

// Version returns the monotonically increasing index version.
func (s *Snapshot) Version() uint64 {
	if s == nil || s.state == nil {
		return 0
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return s.state.version
}

// Retired reports whether the manager has published a newer snapshot.
func (s *Snapshot) Retired() bool {
	if s == nil || s.state == nil {
		return true
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return s.state.retired
}

func (s *Snapshot) acquireIndex() (BEIndex, error) {
	if s == nil || s.state == nil {
		return nil, ErrSnapshotClosed
	}
	s.mu.Lock()
	released := s.released
	s.mu.Unlock()
	if released {
		return nil, ErrSnapshotClosed
	}

	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.index == nil || s.state.refs == 0 {
		return nil, ErrSnapshotClosed
	}
	return s.state.index, nil
}

// Retrieve executes a query against this snapshot.
func (s *Snapshot) Retrieve(queries Assignments, opts ...IndexOpt) (DocIDList, error) {
	index, err := s.acquireIndex()
	if err != nil {
		return nil, err
	}
	return index.Retrieve(queries, opts...)
}

// RetrieveWithCollector executes a query against this snapshot using a caller
// supplied collector.
func (s *Snapshot) RetrieveWithCollector(queries Assignments, collector ResultCollector, opts ...IndexOpt) error {
	index, err := s.acquireIndex()
	if err != nil {
		return err
	}
	return index.RetrieveWithCollector(queries, collector, opts...)
}

// Close releases this caller's reference. It is idempotent and does not close
// other handles or the manager's reference to the same snapshot.
func (s *Snapshot) Close() {
	if s == nil || s.state == nil {
		return
	}
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return
	}
	s.released = true
	state := s.state
	s.mu.Unlock()
	state.release()
}

// SnapshotBuilder owns an independent mutable IndexerBuilder. It cannot be
// queried directly; only Publish turns its built index into a Snapshot.
type SnapshotBuilder struct {
	mu sync.Mutex

	builder *IndexerBuilder
	closed  bool
	built   bool
}

func (b *SnapshotBuilder) ConfigField(field BEField, settings FieldOption) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureOpen()
	if b.built {
		panic(ErrBuildClosed)
	}
	b.builder.ConfigField(field, settings)
}

func (b *SnapshotBuilder) AddDocument(docs ...*Document) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkOpen(); err != nil {
		return err
	}
	if b.built {
		return ErrBuildClosed
	}
	return b.builder.AddDocument(docs...)
}

func (b *SnapshotBuilder) build() (index BEIndex, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err = b.checkOpen(); err != nil {
		return nil, err
	}
	if b.built {
		return nil, ErrBuildClosed
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("build snapshot: %v", recovered)
			index = nil
		}
	}()
	index = b.builder.BuildIndex()
	b.built = true
	return index, nil
}

// Close abandons this build. A closed or failed build can never be published.
func (b *SnapshotBuilder) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

func (b *SnapshotBuilder) ensureOpen() {
	if err := b.checkOpen(); err != nil {
		panic(err)
	}
}

func (b *SnapshotBuilder) checkOpen() error {
	if b == nil || b.closed || b.builder == nil {
		return ErrBuildClosed
	}
	return nil
}

// SnapshotManager owns the current snapshot. Build work happens outside the
// manager lock; Publish atomically changes which immutable snapshot new readers
// observe.
type SnapshotManager struct {
	mu sync.RWMutex

	current *snapshotState
	nextVer uint64
	closed  bool
}

func NewSnapshotManager() *SnapshotManager {
	return &SnapshotManager{}
}

// BeginBuild creates an isolated builder. Until Publish succeeds, queries only
// see the manager's previous snapshot.
func (m *SnapshotManager) BeginBuild(opts ...BuilderOpt) (*SnapshotBuilder, error) {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, ErrSnapshotManagerClosed
	}
	return &SnapshotBuilder{builder: NewIndexerBuilder(opts...)}, nil
}

// Current returns a retained caller-owned handle to the current snapshot.
func (m *SnapshotManager) Current() (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrSnapshotManagerClosed
	}
	if m.current == nil || !m.current.retain() {
		return nil, ErrNoCurrentSnapshot
	}
	return &Snapshot{state: m.current}, nil
}

// Publish builds and atomically installs a new snapshot. The build is fully
// completed before the current snapshot is replaced.
func (m *SnapshotManager) Publish(builder *SnapshotBuilder) (*Snapshot, error) {
	if builder == nil {
		return nil, ErrBuildClosed
	}
	index, err := builder.build()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrSnapshotManagerClosed
	}
	m.nextVer++
	state := &snapshotState{
		index:   index,
		version: m.nextVer,
		refs:    2, // manager ownership plus the returned caller handle
	}
	old := m.current
	m.current = state
	if old != nil {
		old.retire()
	}
	return &Snapshot{state: state}, nil
}

// Close stops future builds and releases the manager's reference to the
// current snapshot. Existing caller-held handles remain valid until Close.
func (m *SnapshotManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	current := m.current
	m.current = nil
	m.mu.Unlock()
	if current != nil {
		current.retire()
	}
}
