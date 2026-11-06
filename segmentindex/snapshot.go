package segmentindex

import (
	"errors"
	"sync"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
)

var (
	ErrManagerClosed = errors.New("segment snapshot manager is closed")
	ErrNoSnapshot    = errors.New("no current segment snapshot")
	ErrSnapshotDone  = errors.New("segment snapshot is closed")
	ErrBuildDone     = errors.New("segment build is closed")
)

type snapshotState struct {
	mu      sync.Mutex
	index   *Index
	version uint64
	refs    int
	retired bool
}

// Snapshot is a caller-owned immutable Segment view.
type Snapshot struct {
	mu       sync.Mutex
	state    *snapshotState
	released bool
}

func (s *Snapshot) Version() uint64 {
	if s == nil || s.state == nil {
		return 0
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return s.state.version
}

func (s *Snapshot) Retired() bool {
	if s == nil || s.state == nil {
		return true
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return s.state.retired
}

func (s *Snapshot) indexOrError() (*Index, error) {
	if s == nil || s.state == nil {
		return nil, ErrSnapshotDone
	}
	s.mu.Lock()
	released := s.released
	s.mu.Unlock()
	if released {
		return nil, ErrSnapshotDone
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.index == nil {
		return nil, ErrSnapshotDone
	}
	return s.state.index, nil
}

func (s *Snapshot) Retrieve(query planner.Query) ([]be.DocID, error) {
	index, err := s.indexOrError()
	if err != nil {
		return nil, err
	}
	return index.Retrieve(query)
}

func (s *Snapshot) Explain(query planner.Query) ([]planner.ExplainResult, error) {
	index, err := s.indexOrError()
	if err != nil {
		return nil, err
	}
	return index.Explain(query)
}

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
		s.index = nil
	}
}

func (s *snapshotState) retire() {
	s.mu.Lock()
	s.retired = true
	s.mu.Unlock()
	s.release()
}

// SnapshotBuilder owns a private mutable exact-term builder.
type SnapshotBuilder struct {
	mu      sync.Mutex
	builder *Builder
	closed  bool
	built   bool
}

func (b *SnapshotBuilder) AddDocuments(documents ...*be.Document) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b == nil || b.closed || b.builder == nil || b.built {
		return ErrBuildDone
	}
	for _, document := range documents {
		if err := b.builder.AddDocument(document); err != nil {
			return err
		}
	}
	return nil
}

func (b *SnapshotBuilder) Seal() (*Index, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b == nil || b.closed || b.builder == nil || b.built {
		return nil, ErrBuildDone
	}
	index, err := b.builder.Seal()
	if err != nil {
		return nil, err
	}
	b.built = true
	return index, nil
}

func (b *SnapshotBuilder) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

// SnapshotManager publishes immutable Segment indexes atomically.
type SnapshotManager struct {
	mu      sync.RWMutex
	current *snapshotState
	nextVer uint64
	closed  bool
}

func NewSnapshotManager() *SnapshotManager {
	return &SnapshotManager{}
}

func (m *SnapshotManager) BeginBuild() (*SnapshotBuilder, error) {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, ErrManagerClosed
	}
	return &SnapshotBuilder{builder: NewBuilder()}, nil
}

func (m *SnapshotManager) Current() (*Snapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if m.current == nil || !m.current.retain() {
		return nil, ErrNoSnapshot
	}
	return &Snapshot{state: m.current}, nil
}

func (m *SnapshotManager) Publish(builder *SnapshotBuilder) (*Snapshot, error) {
	if builder == nil {
		return nil, ErrBuildDone
	}
	index, err := builder.Seal()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	m.nextVer++
	state := &snapshotState{index: index, version: m.nextVer, refs: 2}
	old := m.current
	m.current = state
	if old != nil {
		old.retire()
	}
	return &Snapshot{state: state}, nil
}

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
