package builder

import (
	"fmt"
	"io"
	"sort"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

type postingSink interface {
	AddRecord(field string, record any, entries []core.EntryID) error
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
	// IgnoreUnindexedFields tolerates documents that reference fields absent from
	// the schema. Default (false) fails the build; see NormalizeOptions.
	IgnoreUnindexedFields bool
}

func (o BuildSegmentFromDocsOptions) normalizeOptions() NormalizeOptions {
	return NormalizeOptions{IgnoreUnindexedFields: o.IgnoreUnindexedFields}
}

// BuildSegmentsFromDocs exports docs into one or multiple mmap segments.
//
// It calls newWriter for each segment (segIdx starts from 0).
// Every Segment v4 embeds its own wildcard entries.
func BuildSegmentsFromDocs(
	newWriter func(segIdx int) (io.Writer, error),
	fieldsData map[core.BEField]*core.FieldMeta,
	docs []*core.Document,
	opt BuildSegmentsFromDocsOptions,
) (int, error) {
	codec, err := parser.NewSchemaCodec(fieldsData)
	if err != nil {
		return 0, err
	}
	// Validate the complete corpus before creating any writer. This makes the
	// failure boundary identical for single- and multi-segment builds.
	if err := validateUniqueDocIDs(docs); err != nil {
		return 0, err
	}
	if opt.MaxDocsPerSegment <= 0 || opt.MaxDocsPerSegment >= len(docs) {
		w, err := newWriter(0)
		if err != nil {
			return 0, err
		}
		err = writeSegmentFromDocsWithCodec(w, codec, docs, BuildSegmentFromDocsOptions{})
		if err != nil {
			return 0, err
		}
		return 1, nil
	}
	segIdx := 0
	for start := 0; start < len(docs); start += opt.MaxDocsPerSegment {
		end := start + opt.MaxDocsPerSegment
		if end > len(docs) {
			end = len(docs)
		}
		chunk := docs[start:end]

		w, err := newWriter(segIdx)
		if err != nil {
			return segIdx, err
		}

		// Reuse the single-segment path so each segment embeds a sorted
		// __wildcards block (SetWildcards + Write). Loader recovery depends
		// exclusively on these per-segment wildcard blocks.
		err = writeSegmentFromDocsWithCodec(w, codec, chunk, BuildSegmentFromDocsOptions{})
		if err != nil {
			return segIdx + 1, err
		}
		segIdx++
	}
	return segIdx, nil
}

func validateUniqueDocIDs(docs []*core.Document) error {
	seen := roaring64.New()
	for _, doc := range docs {
		if doc == nil {
			return fmt.Errorf("nil document")
		}
		key := docIDKey(doc.ID)
		if seen.Contains(key) {
			return fmt.Errorf("duplicate doc id %d in full build", doc.ID)
		}
		seen.Add(key)
	}
	return nil
}

// BuildSegmentFromDocs exports documents into a v4 segment. Z-Entries and block
// checksums are always embedded by the writer; opts carries schema metadata and
// normalization behavior.
func BuildSegmentFromDocs(w io.Writer, fieldsData map[core.BEField]*core.FieldMeta, docs []*core.Document, opts BuildSegmentFromDocsOptions) error {
	codec, err := parser.NewSchemaCodec(fieldsData)
	if err != nil {
		return err
	}
	return buildSegmentFromDocsWithCodec(w, codec, docs, opts)
}

func buildSegmentFromDocsWithCodec(w io.Writer, codec *parser.SchemaCodec, docs []*core.Document, opts BuildSegmentFromDocsOptions) error {
	if err := validateUniqueDocIDs(docs); err != nil {
		return err
	}
	return writeSegmentFromDocsWithCodec(w, codec, docs, opts)
}

// writeSegmentFromDocsWithCodec writes a corpus whose DocIDs were already
// validated as globally unique by the owning build operation.
func writeSegmentFromDocsWithCodec(w io.Writer, codec *parser.SchemaCodec, docs []*core.Document, opts BuildSegmentFromDocsOptions) error {
	if err := validateCodecContainers(codec); err != nil {
		return err
	}
	sw := segment.NewInMemorySegmentBuilderWithOptions(w, segment.InMemorySegmentBuilderOptions{
		SchemaHash: opts.SchemaHash,
	})
	sw.SetDocCount(len(docs))

	// Config fields from the compiled schema (single source of truth).
	for _, fc := range codec.Fields() {
		if err := sw.AddField(fc.Meta); err != nil {
			return err
		}
	}

	wildcardEIDs, err := exportDocsToSegment(sw, codec, docs, opts.normalizeOptions())
	if err != nil {
		return err
	}

	sort.Slice(wildcardEIDs, func(i, j int) bool {
		return wildcardEIDs[i] < wildcardEIDs[j]
	})
	sw.SetWildcards(wildcardEIDs)

	return sw.Write()
}

func exportDocsToSegment(sw *segment.InMemorySegmentBuilder, codec *parser.SchemaCodec, docs []*core.Document, normOpt NormalizeOptions) (core.Entries, error) {
	var wildcardEIDs core.Entries

	// Iterate over all documents
	for _, doc := range docs {
		encoded, err := encodeDocument(codec, doc, normOpt)
		if err != nil {
			return nil, err
		}
		if err := commitEncodedDoc(sw, encoded); err != nil {
			return nil, err
		}
		wildcardEIDs = append(wildcardEIDs, encoded.wildcards...)
	}

	return wildcardEIDs, nil
}

// encodedDoc is a fully-encoded document held in a document-local buffer before
// being committed to the sink. Pre-encoding makes commit atomic (§4.1.4): a
// per-predicate encode failure aborts the whole document without ever touching
// the sink, so no half-document is written.
type encodedDoc struct {
	records   []encodedRecord
	wildcards core.Entries
}

type encodedRecord struct {
	field  string
	record any
	eid    core.EntryID
}

// encodeDocument validates+normalizes the document and encodes every predicate
// into a document-local buffer. All errors here are doc-level (isolatable): the
// sink is not touched, so the caller may skip the document under FailSkip
// without leaving partial state.
func encodeDocument(codec *parser.SchemaCodec, doc *core.Document, normOpt NormalizeOptions) (encodedDoc, error) {
	norm, err := ValidateAndNormalizeDocument(codec, doc, normOpt)
	if err != nil {
		return encodedDoc{}, err
	}

	var out encodedDoc
	for conjIdx, conj := range norm.Cons {
		// K is computed from the NORMALIZED conjunction so dropped unindexed
		// fields never inflate it (§4.1.2).
		incSize := conj.CalcConjSize()
		if !core.ValidIdxOrSize(conjIdx) || !core.ValidIdxOrSize(incSize) {
			return encodedDoc{}, fmt.Errorf("doc %d conjunction %d invalid encoding size: K=%d", norm.ID, conjIdx, incSize)
		}
		conjID := core.NewConjID(norm.ID, conjIdx, incSize)

		if incSize == 0 {
			out.wildcards = append(out.wildcards, core.NewEntryID(conjID, true))
		}

		// Iterate over predicates. PredicateEncoder is the only layer that knows
		// how a field's logical ValueExpr maps to physical segment keys; the
		// exporter only assigns EntryIDs and buffers those encoded keys.
		for field, exprs := range conj.Predicates {
			fieldCodec, ok := codec.Field(field)
			if !ok {
				// Only reachable when IgnoreUnindexedFields is set; the field was
				// already dropped from a normalized copy, so this is defensive.
				continue
			}
			for _, expr := range exprs {
				eid := core.NewEntryID(conjID, expr.Incl)
				postings, err := fieldCodec.Encoder.Build(expr)
				if err != nil {
					return encodedDoc{}, fmt.Errorf("field %s encode predicate: %w", field, err)
				}
				for _, posting := range postings {
					out.records = append(out.records, encodedRecord{field: string(field), record: posting.Record, eid: eid})
				}
			}
		}
	}
	return out, nil
}

// commitEncodedDoc writes a fully-encoded document to the sink in one shot.
// A failure here is builder-level (e.g. spill IO), not isolatable: the document
// was already validated, so an error means the sink itself failed.
func commitEncodedDoc(sink postingSink, doc encodedDoc) error {
	for _, r := range doc.records {
		if err := sink.AddRecord(r.field, r.record, []core.EntryID{r.eid}); err != nil {
			return err
		}
	}
	return nil
}
