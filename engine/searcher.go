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

// Retrieve returns matched DocIDs for the given assignments.
// Uses an internal pooled collector; for custom collection use RetrieveWithCollector.
func (e *BooleanEngine) Retrieve(
	queries core.Assignments, opts ...core.IndexOpt,
) (core.DocIDList, error) {
	collector := core.PickCollector()
	defer core.PutCollector(collector)
	if err := e.RetrieveWithCollector(queries, collector, opts...); err != nil {
		return nil, err
	}
	return collector.GetDocIDs(), nil
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
	encoded := e.encodeQueries(queries)

	fCursors := e.initCursors(encoded, ctx.Observer)
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
// The schema is already validated at construction time, and per-field encoding
// errors (e.g. a value that does not fit the field type) simply skip that field,
// so this never fails.
func (e *BooleanEngine) encodeQueries(queries core.Assignments) []encodedField {
	encoded := make([]encodedField, 0, len(queries))
	for field, values := range queries {
		fieldCodec, ok := e.schemaCodec.Field(field)
		if !ok {
			continue
		}
		encodedQueries, err := fieldCodec.Encoder.Query(values)
		if err != nil || len(encodedQueries) == 0 {
			continue
		}
		encoded = append(encoded, encodedField{field: field, queries: encodedQueries})
	}
	return encoded
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
// It requests full posting lists from each segment; each cursor contains
// entries for every K. The mergeCursors function reads K dynamically from
// EntryID as cursors are merged.
func (e *BooleanEngine) initCursors(
	encoded []encodedField, obs core.RetrieveObserver,
) *core.FieldCursors {
	fCursors := core.NewFieldCursors(len(encoded) + 1)

	if len(e.wildcardEntries) > 0 {
		pl := core.NewSliceIterator(core.WildcardTerm, e.wildcardEntries)
		fCursors.Append(core.NewFieldCursor(pl))
	}

	fieldCount := 0
	for _, ef := range encoded {
		field := ef.field
		var iterators []core.PostingIterator

		for _, seg := range e.segments {
			for _, q := range ef.queries {
				switch q.Kind {
				case parser.QueryKindTerm:
					it, err := seg.GetPostingsByTerm(field, q.Term)
					if err == nil && it != nil {
						iterators = append(iterators, it)
					}
				case parser.QueryKindRange:
					iters, err := seg.GetRangePostings(field, q.Point)
					if err == nil && len(iters) > 0 {
						iterators = append(iterators, iters...)
					}
				case parser.QueryKindAC:
					iters, err := seg.MultiPatternSearch(field, q.Text)
					if err == nil && len(iters) > 0 {
						iterators = append(iterators, iters...)
					}
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

	return fCursors
}

// DumpIndexInfo writes diagnostic information about the engine.
func (e *BooleanEngine) DumpIndexInfo(sb *strings.Builder) {
	sb.WriteString("\n+++++++ Mmap Searcher info +++++++++++\n")
	sb.WriteString(fmt.Sprintf("wildcard info: count:%d\n", len(e.wildcardEntries)))
	sb.WriteString(fmt.Sprintf("segments count: %d\n", len(e.segments)))
	sb.WriteString("\n++++++++++++++dump index info end ++++++++++++++++\n")
}
