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

	// Encode every assignment once, before the K loop. Query value encoding does
	// not depend on K, so doing it inside initCursors would repeat the same
	// tokenization (and its allocations) maxK+1 times per field.
	encoded := e.encodeQueries(queries)

	maxK := len(queries)
	for k := maxK; k >= 0; k-- {
		fCursors := e.initCursors(k, encoded, ctx.Observer)
		if fCursors.Len() == 0 {
			continue
		}
		needMatchCnt := k
		if needMatchCnt == 0 {
			needMatchCnt = 1
		}
		e.retrieveK(&ctx, fCursors, needMatchCnt)
	}
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

// retrieveK performs the K-Groups multiway merge algorithm from VLDB 09.
// FieldCursors is maintained as a min-heap: Peek/AdvanceFirst are O(k log n)
// instead of the previous O(n log n) sort.Slice per iteration.
func (e *BooleanEngine) retrieveK(
	ctx *core.RetrieveContext, fieldCursors *core.FieldCursors, needMatchCnt int,
) {
	if fieldCursors.Len() < needMatchCnt {
		return
	}

	obs := ctx.Observer

	for !fieldCursors.PeekAt(needMatchCnt - 1).IsNULLEntry() {
		eid := fieldCursors.Peek()
		endEID := fieldCursors.PeekAt(needMatchCnt - 1)

		conjID := eid.GetConjID()
		endConjID := endEID.GetConjID()

		nextID := core.NewEntryID(endConjID, false)

		if conjID == endConjID {
			nextID = core.NewEntryID(endEID.GetConjID(), true) + 1

			if eid.IsInclude() {
				if e.liveDocs == nil || e.liveDocs.IsAlive(conjID.DocID()) {
					ctx.Collector.Add(conjID.DocID(), conjID)
					if obs != nil {
						obs.OnMatch(conjID.DocID(), conjID)
					}
				}
			} else {
				if obs != nil {
					obs.OnExcludeSkip(conjID.DocID())
				}
				fieldCursors.ShortCircuitAfter(needMatchCnt, nextID)
			}
		}

		fieldCursors.AdvanceFirst(needMatchCnt, nextID)
	}
}

// initCursors builds the FieldCursors for a given K value from pre-encoded
// query keys. It performs no value encoding (done once in encodeQueries) and
// therefore cannot fail; unknown encoded kinds are defensively skipped.
func (e *BooleanEngine) initCursors(
	k int, encoded []encodedField, obs core.RetrieveObserver,
) *core.FieldCursors {
	fCursors := core.NewFieldCursors(len(encoded) + 1)

	if k == 0 && len(e.wildcardEntries) > 0 {
		var kWildcards []core.EntryID
		for _, eid := range e.wildcardEntries {
			if eid.GetConjID().Size() == 0 {
				kWildcards = append(kWildcards, eid)
			}
		}
		if len(kWildcards) > 0 {
			pl := core.NewSliceIterator(core.WildcardTerm, kWildcards)
			fCursors.Append(core.NewFieldCursor(pl))
		}
	}

	fieldCount := 0
	for _, ef := range encoded {
		field := ef.field
		var iterators []core.PostingIterator

		for _, seg := range e.segments {
			for _, q := range ef.queries {
				switch q.Kind {
				case parser.QueryKindTerm:
					it, err := seg.GetPostingsByTerm(k, field, q.Term)
					if err == nil && it != nil {
						iterators = append(iterators, it)
					}
				case parser.QueryKindRange:
					iters, err := seg.GetRangePostings(k, field, q.Point)
					if err == nil && len(iters) > 0 {
						iterators = append(iterators, iters...)
					}
				case parser.QueryKindAC:
					iters, err := seg.MultiPatternSearch(k, field, q.Text)
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
		obs.OnCursorInit(k, fieldCount)
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
