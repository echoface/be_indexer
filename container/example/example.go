// Package example is a minimal, end-to-end template showing how library
// users plug a custom index container + predicate encoder into be_indexer
// WITHOUT touching engine/segment/builder code:
//
//  1. Implement parser.PredicateEncoder: translate business predicates into
//     EncodedPosting (build) and EncodedQuery (query).
//  2. Implement segment.IndexBuilder/IndexReader: serialize the
//     per-field term→PostingRef table into a byte block and answer queries
//     against it with zero-copy posting cursors.
//  3. Register both in init() under the same name; users select it via
//     FieldMeta.FieldOption{IndexType: IndexName}.
//
// Data flow:
//
//	Build:  doc_exporter → sink.AddRecord(field, record, entries)
//	        → segment writer → IndexBuilder.AddRecord(record, entries) → block bytes
//	Query:  engine initCursors
//	        → SegmentReader.IndexQuery(field, containerName, Value)
//	        → IndexReader.MatchQuery → PostingIterators → K-Groups merge
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

// IndexName selects this index/encoder pair in FieldMeta.Index.
const IndexName = "example_prefix"

// --- encoder (build/query translation boundary) ---

// Encoder implements parser.PredicateEncoder. Build-side values are prefix
// strings; query-side values are full strings tested against those prefixes.
type Encoder struct{}

var _ parser.PredicateEncoder = Encoder{}

// Build emits one posting per distinct prefix pattern. The segment writer
// hands every (term, PostingRef) of the field to our IndexBuilder.
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
// via the field's schema IndexType, so the encoder only produces values.
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

// PrefixBuilder accumulates entries via AddRecord (full pipeline) or AddPosting (direct PostingRef).
type PrefixBuilder struct {
	collector *segment.KeyedPostingCollector
	terms     []termRef // direct PostingRef references (test/advanced path)
}

type termRef struct {
	term string
	ref  segment.PostingRef
}

func NewPrefixBuilder(env segment.BuilderEnv) *PrefixBuilder {
	return &PrefixBuilder{
		collector: segment.NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
}

func (b *PrefixBuilder) AddRecord(record any, entries []core.EntryID) error {
	term, ok := record.(string)
	if !ok {
		termBytes, ok2 := record.([]byte)
		if !ok2 {
			return fmt.Errorf("PrefixBuilder: expected string or []byte record, got %T", record)
		}
		return b.collector.Add(termBytes, entries)
	}
	return b.collector.Add([]byte(term), entries)
}

func (b *PrefixBuilder) AddPosting(term string, ref segment.PostingRef) {
	b.terms = append(b.terms, termRef{term: term, ref: ref})
}

func (b *PrefixBuilder) Build(bw segment.BlockWriter) error {
	if b.collector != nil {
		sorted, err := b.collector.Merge()
		if err != nil {
			return err
		}
		if len(sorted) > 0 {
			writer := &segment.DictPostingsWriter{}
			refs, err := writer.Write(sorted, bw)
			if err != nil {
				return err
			}
			for _, rec := range sorted {
				b.terms = append(b.terms, termRef{term: string(rec.Key), ref: refs[string(rec.Key)]})
			}
		}
	}
	if len(b.terms) == 0 {
		return nil
	}
	sort.Slice(b.terms, func(i, j int) bool { return b.terms[i].term < b.terms[j].term })
	size := 4
	for _, t := range b.terms {
		if len(t.term) > 255 {
			return fmt.Errorf("example: term %q longer than 255 bytes", t.term)
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
	return bw.WriteBlock(IndexName, buf)
}

// --- query side (zero-copy reader) ---

// PrefixIndex answers prefix queries over the serialized block. Created once at
// SegmentReader construction; MatchQuery must be safe for concurrent use
// (read-only state).
type PrefixIndex struct {
	entries []termRef
}

// NewPrefixReader decodes the block produced by PrefixBuilder.Build.
func NewPrefixReader(b []byte) (segment.IndexReader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("example: truncated header")
	}
	count := binary.LittleEndian.Uint32(b[0:4])
	r := &PrefixIndex{entries: make([]termRef, 0, count)}
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
func (r *PrefixIndex) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
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
	parser.RegisterPredicateEncoder(IndexName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
	segment.RegisterIndex(IndexName, segment.IndexDef{
		Reader:  func(b []byte) (segment.IndexReader, error) { return NewPrefixReader(b) },
		Builder: func(env segment.BuilderEnv) segment.IndexBuilder { return NewPrefixBuilder(env) },
	})
}
