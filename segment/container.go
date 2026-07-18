package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// ContainerReader reads a serialized container block and provides query access.
// Instances are created during SegmentReader construction (cold path, once per
// block) and queried during retrieval (hot path). Implementations must be safe
// for concurrent Retrieve calls.
//
// postingBlock is the field's posting block bytes, from which containers can
// create zero-copy PostingCursor views without carrying the block as mutable state.
type ContainerReader interface {
	Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error)
}

// ContainerBuilder receives records and their associated EntryIDs during segment
// construction. Build is called once per field after all records are consumed;
// the container writes its own block bytes.
type ContainerBuilder interface {
	Build() ([]byte, error)
}

// BatchBuilder is for containers that accumulate records in document insertion
// order. The framework calls AddPosting for each record; after all records are
// consumed, the framework calls Build.
type BatchBuilder interface {
	ContainerBuilder
	AddPosting(record any, entries []core.EntryID) error
}

// SortableBuilder is for containers whose build benefits from framework-managed
// external sort and grouping by key. The framework sorts records by (field, key,
// entry), groups by key, writes a shared FlatPostingList per group, and calls
// AddKeyedPosting with the resulting PostingRef.
//
// Only one of BatchBuilder or SortableBuilder should be implemented per container.
// The framework detects the interface via type assertion: SortableBuilder first,
// then BatchBuilder, then Dict fallback.
type SortableBuilder interface {
	ContainerBuilder
	RecordToKey(record any) []byte
	AddKeyedPosting(key []byte, ref PostingRef, entries []core.EntryID) error
}

// BlockWriter is the interface through which containers write blocks during Build.
// The framework tracks offset, alignment, checksum, and BlockIndex registration.
type BlockWriter interface {
	WriteBlock(kind string, data []byte) error
}

// ContainerReaderFactory creates a ContainerReader from serialized block bytes.
type ContainerReaderFactory func(blockBytes []byte) (ContainerReader, error)

// ContainerBuilderFactory creates a fresh ContainerBuilder.
type ContainerBuilderFactory func() ContainerBuilder

// ContainerDef describes a registered container implementation.
// Builder and Reader may be nil: a nil Builder means the container uses
// the default Dict posting path (pure term posting without a separate
// container block). A nil Reader means queries for this kind are not
// dispatched via ContainerQuery.
type ContainerDef struct {
	Reader  ContainerReaderFactory
	Builder ContainerBuilderFactory
}

var containers = map[string]ContainerDef{}

// RegisterContainer installs a named container implementation. kind is the
// container name used in FieldMeta.Container (e.g. "ac_matcher", "ext_range").
// Registration must happen before schema compilation or segment loading;
// it is not safe for concurrent use.
func RegisterContainer(kind string, def ContainerDef) {
	if _, dup := containers[kind]; dup {
		panic(fmt.Sprintf("RegisterContainer(%q): duplicate registration", kind))
	}
	containers[kind] = def
}

// NewContainerReader creates a ContainerReader for the given kind from serialized
// block bytes.
func NewContainerReader(kind string, blockBytes []byte) (ContainerReader, error) {
	e, ok := containers[kind]
	if !ok {
		return nil, fmt.Errorf("unknown container kind %q", kind)
	}
	if e.Reader == nil {
		return nil, fmt.Errorf("container kind %q has no reader", kind)
	}
	return e.Reader(blockBytes)
}

// NewContainerBuilder creates a fresh ContainerBuilder for the given kind.
// Returns (nil, nil) when the kind is registered but has no Builder
// (pure Dict posting path). Returns error when the kind is not registered.
func NewContainerBuilder(kind string) (ContainerBuilder, error) {
	def, ok := containers[kind]
	if !ok {
		return nil, fmt.Errorf("unknown container kind %q", kind)
	}
	if def.Builder == nil {
		return nil, nil
	}
	return def.Builder(), nil
}

// HasContainer reports whether a container kind is registered.
func HasContainer(kind string) bool {
	_, ok := containers[kind]
	return ok
}
