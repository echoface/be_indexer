package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// BlockContext carries the posting block reference so index readers can produce
// zero-copy PostingIterators at query time.
type BlockContext struct {
	Pl []byte
}

// IndexReader reads a serialized index block and provides query access.
// Instances are created during SegmentReader construction (cold path, once per
// block) and queried during retrieval (hot path). Implementations must be safe
// for concurrent MatchQuery calls.
type IndexReader interface {
	MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error)
}

// IndexBuilder is responsible for a field's entire indexing logic during
// segment construction. AddRecord is called for each (record, entries) pair in
// document insertion order. Build is called once per field after all records
// have been added; the index writes its blocks via the provided BlockWriter.
type IndexBuilder interface {
	AddRecord(record any, entries []core.EntryID) error
	Build(bw BlockWriter) error
}

// BlockWriter allows index builders to write named blocks into the segment.
// The framework derives the full block name from the current field name and
// kind (e.g. kind="dict" on field "age" becomes "age_dict"). The framework
// handles 8-byte alignment, offset tracking, checksums, and MetaBlock
// registration.
type BlockWriter interface {
	WriteBlock(kind string, data []byte) error
}

// BuilderEnv carries build-time configuration from the framework to index
// builders.
type BuilderEnv struct {
	MaxPostingsInMemory int    // <= 0 disables spill (in-memory only)
	TmpDir              string // spill file directory when MaxPostingsInMemory > 0
}

// IndexReaderFactory creates an IndexReader from serialized block bytes.
type IndexReaderFactory func(blockBytes []byte) (IndexReader, error)

// IndexBuilderFactory creates an IndexBuilder with the given environment.
type IndexBuilderFactory func(env BuilderEnv) IndexBuilder

// IndexDef describes a registered index implementation.
type IndexDef struct {
	Reader  IndexReaderFactory
	Builder IndexBuilderFactory
}

var registry = map[string]IndexDef{}

// RegisterIndex installs a named index kind. kind is the value used in
// FieldMeta.Index (e.g. "default", "ac_matcher", "ext_range").
func RegisterIndex(kind string, def IndexDef) {
	if _, dup := registry[kind]; dup {
		panic(fmt.Sprintf("RegisterIndex(%q): duplicate registration", kind))
	}
	registry[kind] = def
}

// NewIndexReader creates an IndexReader for the given kind.
func NewIndexReader(kind string, blockBytes []byte) (IndexReader, error) {
	e, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown index kind %q", kind)
	}
	if e.Reader == nil {
		return nil, fmt.Errorf("index kind %q has no reader", kind)
	}
	return e.Reader(blockBytes)
}

// NewIndexBuilder creates an IndexBuilder for the given kind and env.
func NewIndexBuilder(kind string, env BuilderEnv) (IndexBuilder, error) {
	def, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown index kind %q", kind)
	}
	if def.Builder == nil {
		return nil, fmt.Errorf("index kind %q has no builder", kind)
	}
	return def.Builder(env), nil
}

// HasIndex reports whether an index kind is registered.
func HasIndex(kind string) bool {
	_, ok := registry[kind]
	return ok
}
