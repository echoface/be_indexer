// Package example is a minimal, end-to-end template showing how library
// users plug a custom index container + predicate encoder into be_indexer
// WITHOUT touching engine/segment/builder code:
//
//  1. Implement parser.PredicateEncoder: translate business predicates into
//     EncodedPosting (build) and EncodedQuery (query).
//  2. Implement segment.ContainerBuilder/ContainerReader: serialize the
//     per-field term→PostingRef table into a byte block and answer queries
//     against it with zero-copy posting cursors.
//  3. Register both in init() under the same name; users select it via
//     FieldMeta.FieldOption{Container: ContainerName}.
//
// Data flow:
//
//	Build:  doc_exporter → sink.AddRecord(field, container, record, entries)
//	        → segment writer → ContainerBuilder.Add(term, ref) → block bytes
//	Query:  engine initCursors
//	        → SegmentReader.ContainerQuery(field, containerName, Value)
//	        → ContainerReader.MatchQuery → PostingIterators → K-Groups merge
//
// The demo semantic: stored terms are path prefixes; a query string matches
// every stored term that prefixes it (e.g. stored "/api" matches query
// "/api/v1/users"). Copy this package as a starting point for real
// containers (interval trees, tries, bloom-gated dictionaries, ...).
package example

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
	"github.com/echoface/be_indexer/util"
)

// ContainerName selects this container/encoder pair in FieldMeta.Container.
const ContainerName = "example_prefix"

// --- encoder (build/query translation boundary) ---

// Encoder implements parser.PredicateEncoder. Build-side values are prefix
// strings; query-side values are full strings tested against those prefixes.
type Encoder struct{}

var _ parser.PredicateEncoder = Encoder{}

// Build emits one posting per distinct prefix pattern. The segment writer
// hands every (term, PostingRef) of the field to our ContainerBuilder.
func (Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("example: nil value expression")
	}
	if expr.Operator != core.ValueOptEQ {
		return nil, fmt.Errorf("%w: example encoder only supports EQ, got %d", core.ErrUnsupportedPredicate, expr.Operator)
	}
	patterns, err := parser.ValuesToStrings(expr.Value)
	if err != nil {
		return nil, err
	}
	patterns = util.DistinctString(patterns)
	sort.Strings(patterns)
	out := make([]parser.EncodedPosting, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			return nil, fmt.Errorf("example: empty prefix pattern")
		}
		if len(p) > 255 {
			return nil, fmt.Errorf("example: pattern longer than 255 bytes")
		}
		out = append(out, parser.EncodedPosting{Record: p})
	}
	return out, nil
}

// Query emits one container lookup per assignment string. The engine routes
// via the field's schema Container, so the encoder only produces values.
func (Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	texts, err := parser.ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	out := make([]parser.EncodedQuery, 0, len(texts))
	for _, s := range texts {
		out = append(out, parser.EncodedQuery{Value: s})
	}
	return out, nil
}

// --- build side (segment serialization) ---

// Builder accumulates (term, PostingRef) pairs via SortableBuilder and
// serializes them. Registered as Sortable=true so the framework sorts
// and groups by key before calling AddKeyedPosting.
//
// Binary layout (all integers little-endian, matching segment conventions):
//
//	[count uint32]
//	repeat count times, sorted by term ascending:
//	  [termLen uint8][term bytes][offset uint64][entryCount uint32]
type Builder struct {
	terms []termRef
}

type termRef struct {
	term string
	ref  segment.PostingRef
}

// NewBuilder creates a fresh builder; the segment writer calls it once per field.
func NewBuilder() segment.ContainerBuilder {
	return &Builder{}
}

func (b *Builder) RecordToKey(record any) []byte {
	switch v := record.(type) {
	case string:
		return []byte(v)
	case []byte:
		return v
	default:
		return nil
	}
}

func (b *Builder) AddKeyedPosting(key []byte, ref segment.PostingRef, entries []core.EntryID) error {
	b.terms = append(b.terms, termRef{term: string(key), ref: ref})
	return nil
}

func (b *Builder) Build() ([]byte, error) {
	sort.Slice(b.terms, func(i, j int) bool { return b.terms[i].term < b.terms[j].term })

	size := 4
	for _, t := range b.terms {
		if len(t.term) > 255 {
			return nil, fmt.Errorf("example: term %q longer than 255 bytes", t.term)
		}
		size += 1 + len(t.term) + 8 + 4
	}
	buf := make([]byte, 0, size)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.terms)))
	for _, t := range b.terms {
		buf = append(buf, uint8(len(t.term)))
		buf = append(buf, t.term...)
		buf = binary.LittleEndian.AppendUint64(buf, t.ref.Offset)
		buf = binary.LittleEndian.AppendUint32(buf, t.ref.Count)
	}
	return buf, nil
}

// --- query side (zero-copy reader) ---

// Reader answers prefix queries over the serialized block. Created once at
// SegmentReader construction; Retrieve must be safe for concurrent use
// (read-only state).
type Reader struct {
	entries []termRef
}

// NewReader decodes the block produced by Builder.Build.
func NewReader(b []byte) (segment.ContainerReader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("example: truncated header")
	}
	count := binary.LittleEndian.Uint32(b[0:4])
	r := &Reader{entries: make([]termRef, 0, count)}
	off := 4
	for i := uint32(0); i < count; i++ {
		if off+1 > len(b) {
			return nil, fmt.Errorf("example: truncated term length at entry %d", i)
		}
		tl := int(b[off])
		off++
		if off+tl+12 > len(b) {
			return nil, fmt.Errorf("example: truncated entry %d", i)
		}
		term := string(b[off : off+tl])
		off += tl
		ref := segment.PostingRef{
			Offset: binary.LittleEndian.Uint64(b[off:]),
			Count:  binary.LittleEndian.Uint32(b[off+8:]),
		}
		off += 12
		r.entries = append(r.entries, termRef{term: term, ref: ref})
	}
	return r, nil
}

// MatchQuery returns posting cursors for every stored prefix that prefixes the
// query string. Linear scan keeps the template simple; production containers
// should exploit their layout (binary search, trie, interval tree...).
func (r *Reader) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	q, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("example: query must be string, got %T", query)
	}
	var iters []core.PostingIterator
	for _, e := range r.entries {
		if !strings.HasPrefix(q, e.term) {
			continue
		}
		pl, err := segment.NewPostingListAt(ctx.Pl, e.ref)
		if err != nil {
			return nil, fmt.Errorf("example: term %q posting: %w", e.term, err)
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, e.term)))
	}
	return iters, nil
}

func init() {
	parser.RegisterPredicateEncoder(ContainerName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
	segment.RegisterContainer(ContainerName, segment.ContainerDef{
		Reader:  func(b []byte) (segment.ContainerReader, error) { return NewReader(b) },
		Builder: func() segment.ContainerBuilder { return NewBuilder() },
	})
}
