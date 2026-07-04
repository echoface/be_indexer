package builder

import (
	"fmt"
	"io"
	"sort"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

type postingSink interface {
	AddPosting(k int, field string, term string, entries []core.EntryID) error
	AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}

// BuildSegmentsFromDocsOptions controls memory usage by splitting build into multiple segments.
//
// Why this helps:
// - segment.InMemorySegmentBuilder materializes all postings in memory before Write().
// - Splitting docs into multiple segments caps peak memory to ~one segment worth of postings.
type BuildSegmentsFromDocsOptions struct {
	// MaxDocsPerSegment limits how many docs are built into one segment.
	// <= 0 means "no limit" (single segment).
	MaxDocsPerSegment int
}

// BuildSegmentFromDocsOptions controls single-segment physical format options.
type BuildSegmentFromDocsOptions struct {
	SchemaHash string
}

// BuildSegmentsFromDocs exports docs into one or multiple mmap segments.
//
// It calls newWriter for each segment (segIdx starts from 0).
// The returned wildcard entries is the union of all segments and should be passed to BooleanEngine.
func BuildSegmentsFromDocs(
	newWriter func(segIdx int) (io.Writer, error),
	fieldsData map[core.BEField]*core.FieldMeta,
	docs []*core.Document,
	opt BuildSegmentsFromDocsOptions,
) (core.Entries, int, error) {
	codec, err := parser.NewSchemaCodec(fieldsData)
	if err != nil {
		return nil, 0, err
	}
	if opt.MaxDocsPerSegment <= 0 || opt.MaxDocsPerSegment >= len(docs) {
		w, err := newWriter(0)
		if err != nil {
			return nil, 0, err
		}
		wildcards, err := buildSegmentFromDocsWithCodec(w, codec, docs, BuildSegmentFromDocsOptions{})
		if err != nil {
			return nil, 0, err
		}
		return wildcards, 1, nil
	}

	var allWildcards core.Entries
	segIdx := 0
	for start := 0; start < len(docs); start += opt.MaxDocsPerSegment {
		end := start + opt.MaxDocsPerSegment
		if end > len(docs) {
			end = len(docs)
		}
		chunk := docs[start:end]

		w, err := newWriter(segIdx)
		if err != nil {
			return nil, segIdx, err
		}

		sw := segment.NewInMemorySegmentBuilder(w)
		sw.SetDocCount(len(chunk))
		for _, fc := range codec.Fields() {
			sw.AddField(fc.Meta)
		}

		wildcards, err := exportDocsToSegment(sw, codec, chunk)
		if err != nil {
			return nil, segIdx + 1, err
		}
		allWildcards = append(allWildcards, wildcards...)

		if err := sw.Write(); err != nil {
			return nil, segIdx + 1, err
		}
		segIdx++
	}

	sort.Slice(allWildcards, func(i, j int) bool {
		return allWildcards[i] < allWildcards[j]
	})
	return allWildcards, segIdx, nil
}

// BuildSegmentFromDocs exports documents directly into a memory-mappable segment
// It returns the wildcard entries that should be passed to BooleanEngine.
func BuildSegmentFromDocs(w io.Writer, fieldsData map[core.BEField]*core.FieldMeta, docs []*core.Document) (core.Entries, error) {
	return BuildSegmentFromDocsWithOptions(w, fieldsData, docs, BuildSegmentFromDocsOptions{})
}

// BuildSegmentFromDocsWithOptions exports documents into a segment with optional
// physical format features such as segment v2 embedded Z-list and block checksums.
func BuildSegmentFromDocsWithOptions(w io.Writer, fieldsData map[core.BEField]*core.FieldMeta, docs []*core.Document, opts BuildSegmentFromDocsOptions) (core.Entries, error) {
	codec, err := parser.NewSchemaCodec(fieldsData)
	if err != nil {
		return nil, err
	}
	return buildSegmentFromDocsWithCodec(w, codec, docs, opts)
}

func buildSegmentFromDocsWithCodec(w io.Writer, codec *parser.SchemaCodec, docs []*core.Document, opts BuildSegmentFromDocsOptions) (core.Entries, error) {
	sw := segment.NewInMemorySegmentBuilderWithOptions(w, segment.InMemorySegmentBuilderOptions{
		SchemaHash: opts.SchemaHash,
	})
	sw.SetDocCount(len(docs))

	// Config fields from the compiled schema (single source of truth).
	for _, fc := range codec.Fields() {
		sw.AddField(fc.Meta)
	}

	wildcardEIDs, err := exportDocsToSegment(sw, codec, docs)
	if err != nil {
		return nil, err
	}

	sort.Slice(wildcardEIDs, func(i, j int) bool {
		return wildcardEIDs[i] < wildcardEIDs[j]
	})
	sw.SetWildcards(wildcardEIDs)

	return wildcardEIDs, sw.Write()
}

func exportDocsToSegment(sw *segment.InMemorySegmentBuilder, codec *parser.SchemaCodec, docs []*core.Document) (core.Entries, error) {
	var wildcardEIDs core.Entries

	// Iterate over all documents
	for _, doc := range docs {
		if doc == nil {
			return nil, fmt.Errorf("nil document")
		}
		wildcards, err := exportDocToSink(sw, codec, doc)
		if err != nil {
			return nil, err
		}
		wildcardEIDs = append(wildcardEIDs, wildcards...)
	}

	return wildcardEIDs, nil
}

func exportDocToSink(sink postingSink, codec *parser.SchemaCodec, doc *core.Document) (core.Entries, error) {
	if doc == nil {
		return nil, fmt.Errorf("nil document")
	}
	if !core.ValidDocID(doc.ID) {
		return nil, fmt.Errorf("invalid doc id %d: exceeds 43-bit encoding range", doc.ID)
	}
	if len(doc.Cons) >= 256 {
		return nil, fmt.Errorf("doc %d has too many conjunctions: %d >= 256", doc.ID, len(doc.Cons))
	}
	var wildcardEIDs core.Entries
	for conjIdx, conj := range doc.Cons {
		incSize := conj.CalcConjSize()
		if !core.ValidIdxOrSize(conjIdx) || !core.ValidIdxOrSize(incSize) {
			return nil, fmt.Errorf("doc %d conjunction %d invalid encoding size: K=%d", doc.ID, conjIdx, incSize)
		}
		conjID := core.NewConjID(doc.ID, conjIdx, incSize)

		if incSize == 0 {
			wildcardEIDs = append(wildcardEIDs, core.NewEntryID(conjID, true))
		}

		// Iterate over predicates. PredicateEncoder is the only layer that knows
		// how a field's logical ValueExpr maps to physical segment keys; the
		// exporter only assigns EntryIDs and writes those encoded keys to the sink.
		for field, exprs := range conj.Predicates {
			fieldCodec, ok := codec.Field(field)
			if !ok {
				continue // Field not indexed
			}

			for _, expr := range exprs {
				eid := core.NewEntryID(conjID, expr.Incl)
				postings, err := fieldCodec.Encoder.Build(expr)
				if err != nil {
					return nil, fmt.Errorf("field %s encode predicate: %w", field, err)
				}
				for _, posting := range postings {
					switch posting.Kind {
					case parser.PostingKindTerm, parser.PostingKindAC:
						if err := sink.AddPosting(incSize, string(field), posting.Term, []core.EntryID{eid}); err != nil {
							return nil, err
						}
					case parser.PostingKindRange:
						if err := sink.AddRangePosting(incSize, string(field), posting.Lo, posting.Hi, eid); err != nil {
							return nil, err
						}
					default:
						return nil, fmt.Errorf("field %s unknown encoded posting kind %q", field, posting.Kind)
					}
				}
			}
		}
	}
	return wildcardEIDs, nil
}
