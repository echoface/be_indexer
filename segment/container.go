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

// ContainerBuilder accumulates terms and their PostingRefs during segment
// construction and compiles them into a serialized byte block. Build is called
// once per field; after Build the builder is discarded.
//
// If the builder also implements ContainerMetaBuilder (an optional interface),
// the segment writer will call AddMeta when the encoder produced a Value in
// its EncodedPosting, allowing custom containers to receive per-term metadata.
type ContainerBuilder interface {
	Add(term string, ref PostingRef)
	Build() ([]byte, error)
}

// ContainerMetaBuilder is an optional interface that ContainerBuilder
// implementations may satisfy. When a PredicateEncoder includes a Value in
// its EncodedPosting, the segment writer passes it through AddMeta so the
// container can capture term-specific build metadata.
type ContainerMetaBuilder interface {
	AddMeta(term string, ref PostingRef, meta any)
}

// ContainerReaderFactory creates a ContainerReader from serialized block bytes.
type ContainerReaderFactory func(blockBytes []byte) (ContainerReader, error)

// ContainerBuilderFactory creates a fresh ContainerBuilder.
type ContainerBuilderFactory func() ContainerBuilder

type containerEntry struct {
	reader  ContainerReaderFactory
	builder ContainerBuilderFactory
}

var containers = map[string]containerEntry{}

// RegisterContainer installs a named container implementation. kind is the
// container name used in FieldMeta.Container (e.g. "ac_matcher", "ext_range").
// Both factories must be non-nil. Registration must happen before schema
// compilation or segment loading; it is not safe for concurrent use.
func RegisterContainer(kind string, reader ContainerReaderFactory, builder ContainerBuilderFactory) {
	if reader == nil {
		panic(fmt.Sprintf("RegisterContainer(%q): nil reader factory", kind))
	}
	if _, dup := containers[kind]; dup {
		panic(fmt.Sprintf("RegisterContainer(%q): duplicate registration", kind))
	}
	containers[kind] = containerEntry{reader: reader, builder: builder}
}

// NewContainerReader creates a ContainerReader for the given kind from serialized
// block bytes. Returns nil, false if the kind is not registered.
func NewContainerReader(kind string, blockBytes []byte) (ContainerReader, error) {
	e, ok := containers[kind]
	if !ok {
		return nil, fmt.Errorf("unknown container kind %q", kind)
	}
	return e.reader(blockBytes)
}

// NewContainerBuilder creates a fresh ContainerBuilder for the given kind.
// Returns nil, false if the kind is not registered.
func NewContainerBuilder(kind string) (ContainerBuilder, error) {
	e, ok := containers[kind]
	if !ok {
		return nil, fmt.Errorf("unknown container kind %q", kind)
	}
	return e.builder(), nil
}

// HasContainer reports whether a container kind is registered.
func HasContainer(kind string) bool {
	_, ok := containers[kind]
	return ok
}
