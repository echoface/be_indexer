package engine

import (
	"fmt"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

// BooleanEngine is a read-only index engine backed by mmap'd segments.
// It is safe for concurrent use across goroutines.
type BooleanEngine struct {
	schemaCodec     *parser.SchemaCodec
	wildcardEntries core.Entries
	segments        []*segment.SegmentReader
	liveDocs        *core.LiveDocs
}

// NewBooleanEngine compiles the schema and creates an engine from pre-built
// segments and wildcard entries. It fails fast on invalid field metadata
// (unknown container, duplicate field id, missing tokenizer) so a misconfigured
// schema is rejected at construction time rather than on the first query.
func NewBooleanEngine(
	fieldsData map[core.BEField]*core.FieldMeta,
	wildcardEntries core.Entries,
	segments []*segment.SegmentReader,
) (*BooleanEngine, error) {
	codec, err := parser.NewSchemaCodec(fieldsData)
	if err != nil {
		return nil, err
	}
	// Validate that every field's IndexType resolves to a registered container.
	// parser.NewSchemaCodec already validated encoders; this additionally rejects
	// an unknown/unregistered IndexType (e.g. a missing side-effect import) at
	// construction time instead of silently falling back to the default container
	// during retrieval.
	metas := make([]core.FieldMeta, 0, len(codec.Fields()))
	for _, fc := range codec.Fields() {
		metas = append(metas, fc.Meta)
	}
	if err := segment.ValidateFieldMetas(metas); err != nil {
		return nil, err
	}
	return &BooleanEngine{
		schemaCodec:     codec,
		wildcardEntries: wildcardEntries,
		segments:        segments,
	}, nil
}

// SetLiveDocs attaches a liveness filter to the engine.
func (e *BooleanEngine) SetLiveDocs(ld *core.LiveDocs) {
	e.liveDocs = ld
}

// Close releases every underlying segment reader (e.g. unmaps mmap'd files).
// Callers must ensure no in-flight query is still running against this engine.
func (e *BooleanEngine) Close() error {
	if e == nil {
		return nil
	}
	var firstErr error
	for _, seg := range e.segments {
		if err := seg.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Retrieve returns matched DocIDs as a BitmapDocSet for the given assignments.
func (e *BooleanEngine) Retrieve(
	queries core.Assignments, opts ...core.IndexOpt,
) (*core.BitmapDocSet, error) {
	collector := core.PickCollector()
	defer core.PutCollector(collector)
	if err := e.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}
	// Clone out of the pooled collector so the result is safe to keep after
	// the collector is recycled.
	return collector.Bitmap().Clone(), nil
}

// RetrieveWithCollector feeds matched DocIDs into the provided collector.
func (e *BooleanEngine) RetrieveWithCollector(
	queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt,
) error {
	ctx := core.NewRetrieveCtx(queries, opts...)
	if ctx.Collector != nil {
		panic("can't specify collector twice")
	}
	ctx.Collector = collector

	if ctx.Observer != nil {
		ctx.Observer.OnRetrieveStart(&ctx)
	}
	defer func() {
		if ctx.Observer != nil {
			ctx.Observer.OnRetrieveEnd(&ctx)
		}
	}()

	// Encode every assignment once. All K values share the same query encoding;
	// the segment layer filters by K internally when needed.
	encoded, err := e.encodeQueries(&ctx, queries)
	if err != nil {
		return err
	}

	fCursors, err := e.initCursors(&ctx, encoded)
	if err != nil {
		return err
	}
	if fCursors.Len() == 0 {
		return nil
	}
	e.mergeCursors(&ctx, fCursors)
	return nil
}

// encodedField pairs a query field with its precomputed physical lookup keys so
// the per-K cursor initialization can reuse them without re-encoding.
type encodedField struct {
	field   core.BEField
	queries []parser.EncodedQuery
}

// encodeQueries translates all assignments into physical lookup keys once.
// The schema is already validated at construction time. A per-field encoding
// error (e.g. a value that does not fit the field type) is reported to the
// context: in strict mode it aborts and returns the error; in lenient mode
// (default) the field is skipped and, if the observer implements
// FieldErrorObserver, surfaced via OnFieldError so it is distinguishable from a
// genuine no-match.
func (e *BooleanEngine) encodeQueries(ctx *core.RetrieveContext, queries core.Assignments) ([]encodedField, error) {
	encoded := make([]encodedField, 0, len(queries))
	for field, values := range queries {
		fieldCodec, ok := e.schemaCodec.Field(field)
		if !ok {
			continue
		}
		encodedQueries, err := fieldCodec.Encoder.Query(values)
		if err != nil {
			if ctx.StrictQuery {
				return nil, fmt.Errorf("field %s query encode: %w", field, err)
			}
			reportFieldError(ctx.Observer, field, err)
			continue
		}
		if len(encodedQueries) == 0 {
			continue
		}
		encoded = append(encoded, encodedField{field: field, queries: encodedQueries})
	}
	return encoded, nil
}

// reportFieldError notifies the observer of a skipped per-field error when it
// opts into FieldErrorObserver. It is a no-op otherwise.
func reportFieldError(obs core.RetrieveObserver, field core.BEField, err error) {
	if obs == nil {
		return
	}
	if fe, ok := obs.(core.FieldErrorObserver); ok {
		fe.OnFieldError(field, err)
	}
}

// mergeCursors performs the K-Groups multiway merge without per-K grouping.
// EntryID stores K in its high bits, so sorting by EntryID groups same-K
// entries naturally. K is read dynamically from each EntryID via conjID.Size().
// Exhausted cursors are compacted out to shrink the working set over time.
func (e *BooleanEngine) mergeCursors(ctx *core.RetrieveContext, fieldCursors *core.FieldCursors) {
	fieldCursors.Sort()
	obs := ctx.Observer

	for fieldCursors.Len() > 0 {
		eid := fieldCursors.Peek()
		conjID := eid.GetConjID()
		stepK := conjID.Size()
		needMatchCnt := stepK
		if needMatchCnt == 0 {
			needMatchCnt = 1
		}

		if needMatchCnt > fieldCursors.Len() {
			break
		}

		endEID := fieldCursors.PeekAt(needMatchCnt - 1)
		endConjID := endEID.GetConjID()

		nextID := core.NewEntryID(endConjID, false)

		if conjID == endConjID {
			// Advance past the current conjunction by computing nextID = (conjID << 4 | 1) + 1.
			// The +1 pushes past the last EntryID for this ConjID (Include bit set to 1),
			// so that subsequent SkipTo calls skip all entries belonging to this conjunction.
			nextID = core.NewEntryID(endEID.GetConjID(), true) + 1

			if eid.IsInclude() {
				if e.liveDocs == nil || e.liveDocs.IsAlive(conjID.DocID()) {
					ctx.Collector.Add(conjID.DocID(), conjID)
					if obs != nil {
						obs.OnMatch(conjID.DocID(), conjID)
					}
				}
			} else {
				// Exclude hit: skip ALL remaining cursors past this DocID.
				// Without this optimization, cursors beyond needMatchCnt would
				// still point to entries for this ConjID, causing false matches.
				if obs != nil {
					obs.OnExcludeSkip(conjID.DocID())
				}
				fieldCursors.ShortCircuitAfter(needMatchCnt, nextID)
			}
		}

		fieldCursors.AdvanceFirst(needMatchCnt, nextID)
		fieldCursors.CompactLast()
	}
}

// initCursors builds FieldCursors for ALL K values in a single pass.
// It collects posting iterators from every segment; both field postings and
// per-segment wildcards are wrapped in FieldCursors whose internal heap
// performs lazy K-way merge at query time — no load-time wildcard merge needed.
//
// A container lookup error is handled per ctx.StrictQuery: strict aborts and
// returns the error; lenient skips the offending (segment, value) lookup and,
// when the observer implements FieldErrorObserver, surfaces it via OnFieldError.
func (e *BooleanEngine) initCursors(
	ctx *core.RetrieveContext, encoded []encodedField,
) (*core.FieldCursors, error) {
	obs := ctx.Observer
	fCursors := core.NewFieldCursors(len(encoded) + 1)

	var wildcardIters []core.PostingIterator
	for _, seg := range e.segments {
		wc := seg.Wildcards()
		if len(wc) > 0 {
			wildcardIters = append(wildcardIters, core.NewSliceIterator(core.WildcardTerm, wc))
		}
	}
	if len(wildcardIters) > 0 {
		fCursors.Append(core.NewFieldCursor(wildcardIters...))
	}

	fieldCount := 0
	for _, ef := range encoded {
		field := ef.field

		// The field's container type is already validated at construction time
		// (NewBooleanEngine → segment.ValidateFieldMetas), so an unknown IndexType
		// can never reach here; there is no silent fallback to the default kind.
		containerName := core.IndexNameDefault
		if fc, ok := e.schemaCodec.Field(field); ok {
			if c := fc.Meta.IndexType; c != "" {
				containerName = c
			}
		}

		var iterators []core.PostingIterator
		for _, seg := range e.segments {
			for _, q := range ef.queries {
				iters, err := seg.IndexQuery(field, containerName, q.Value)
				if err != nil {
					if ctx.StrictQuery {
						return nil, fmt.Errorf("field %s index query: %w", field, err)
					}
					reportFieldError(obs, field, err)
					continue
				}
				if len(iters) > 0 {
					iterators = append(iterators, iters...)
				}
			}
		}

		if len(iterators) > 0 {
			fCursors.Append(core.NewFieldCursor(iterators...))
			fieldCount++
		}
	}

	fCursors.Sort()

	if obs != nil && fieldCount > 0 {
		obs.OnCursorInit(fieldCount)
	}

	return fCursors, nil
}

// DumpIndexInfo writes diagnostic information about the engine.
func (e *BooleanEngine) DumpIndexInfo(sb *strings.Builder) {
	var wcCount int
	for _, seg := range e.segments {
		wcCount += len(seg.Wildcards())
	}
	sb.WriteString("\n+++++++ Mmap Searcher info +++++++++++\n")
	sb.WriteString(fmt.Sprintf("wildcard info: count:%d\n", wcCount))
	sb.WriteString(fmt.Sprintf("segments count: %d\n", len(e.segments)))
	sb.WriteString("\n++++++++++++++dump index info end ++++++++++++++++\n")
}
