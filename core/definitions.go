// Package core defines the foundational types for the be_indexer.
// It contains shared types, cursor algorithms, result collection and scoring primitives.
package core

import (
	"errors"
	"fmt"
	"reflect"
)

// --------------------------------------------------------------------------------
// Constants & Sentinel Errors
// --------------------------------------------------------------------------------

const (
	WildcardFieldName = FieldName("_Z_")

	IndexNameDefault     = "default"
	IndexNameACMatcher   = "ac_matcher"
	IndexNameExtendRange = "ext_range"
)

var (
	ErrFieldNotConfigured       = errors.New("field not configured")
	ErrFieldContainerRequired   = errors.New("field container required")
	ErrUnknownContainer         = errors.New("unknown field container")
	ErrUnsupportedPredicate     = errors.New("unsupported predicate")
	ErrUnknownQueryField        = errors.New("unknown query field")
	ErrFieldIndexMissing        = errors.New("field index missing")
	ErrNilResultCollector       = errors.New("result collector is nil")
	ErrDuplicateResultCollector = errors.New("cannot specify collector twice")
)

// --------------------------------------------------------------------------------
// Field Configuration
// --------------------------------------------------------------------------------

// FieldOption specifies how a field should be indexed.
type FieldOption struct {
	IndexType string // index kind: "default", "ac_matcher", "ext_range"
	Encoder   string // predicate encoder name (empty = "default")
}

// Schema maps each field name to its indexing configuration. The map key is the
// field's sole logical identity; FieldOption contains behavior only.
type Schema map[FieldName]FieldOption

// IndexerSettings holds per-field configuration for an index.
type IndexerSettings struct {
	FieldConfig Schema
}

// RangeRecord is the build-side interval record for ext_range containers.
type RangeRecord struct {
	Lo, Hi int64
}

// --------------------------------------------------------------------------------
// Result Collection
// --------------------------------------------------------------------------------

// CollectResult tells the engine how to proceed after a collector receives a
// matched document.
type CollectResult uint8

const (
	// CollectContinue means the collector is not saturated and the engine
	// should keep traversing.
	CollectContinue CollectResult = iota
	// CollectStop means the collector has its final result (e.g. an unscored
	// limit collector has gathered k unique documents). Scored Top K must keep
	// searching until remaining candidates are proven unable to improve it.
	CollectStop
)

// ResultCollector receives visible matched DocIDs in unspecified order. Engines
// deduplicate IDs before Add unless the collector opts into DuplicateTolerantCollector.
// CollectStop ends the entire retrieval, including all remaining full/delta
// layers: no further Add calls occur. Collectors belong to one retrieval goroutine
// and callers reset reusable collectors before starting another query. If retrieval
// returns an error, a streaming collector may contain partial results.
// Conjunction details are reported separately through RetrieveObserver.OnConjunctionMatch.
type ResultCollector interface {
	// Add feeds one matched DocID and reports whether retrieval should stop.
	Add(id DocID) CollectResult
}

// DuplicateTolerantCollector can consume repeated DocIDs without changing its
// result or consuming additional quota. Returning true avoids the engine's
// deduplication set. This capability is independent of CompetitiveCollector:
// accepting duplicates does not authorize score-based skipping or early stop.
type DuplicateTolerantCollector interface {
	ResultCollector
	AcceptsDuplicates() bool
}

// RetrieveObserver is an optional observer for retrieval instrumentation.
// All methods are called synchronously on the retrieval goroutine.
type RetrieveObserver interface {
	OnRetrieveStart(ctx *RetrieveContext)
	OnRetrieveEnd(ctx *RetrieveContext)
	// OnConjunctionMatch reports each matched conjunction visible to this engine.
	// It runs before collector deduplication and CompositeEngine's layer filtering.
	OnConjunctionMatch(docID DocID, conjID ConjID)
	OnExcludeSkip(docID DocID)
	OnCursorInit(fieldCount int)
}

// FieldErrorObserver is an OPTIONAL extension of RetrieveObserver. When an
// observer also implements it, the engine reports per-field query errors
// (encoder or container lookup failures) through OnFieldError so callers can
// distinguish a genuine no-match from a skipped error even in lenient mode.
// Existing observers that do not implement it keep working unchanged.
type FieldErrorObserver interface {
	OnFieldError(field FieldName, err error)
}

// --------------------------------------------------------------------------------
// Retrieval Context
// --------------------------------------------------------------------------------

// RetrieveContext carries query-time state.
type RetrieveContext struct {
	DumpStepInfo bool
	// StrictQuery controls how per-field query errors (encoder or container
	// lookup failures) are handled. Default (false) is lenient: an errored field
	// is skipped and retrieval continues. When
	// true, the first field error aborts retrieval and is returned to the caller.
	StrictQuery bool
	// DisableScorePruning preserves exact scored Top K but uses only Boolean
	// skips. It can be cheaper for highly selective queries or weak score bounds.
	DisableScorePruning bool
	Collector           ResultCollector
	Assigns             Assignments
	Observer            RetrieveObserver
}

// QueryOption is a functional option for RetrieveContext.
type QueryOption func(ctx *RetrieveContext)

// NewRetrieveCtx creates a context with the given assignments and options.
func NewRetrieveCtx(ass Assignments, opts ...QueryOption) RetrieveContext {
	ctx := RetrieveContext{Assigns: ass}
	for _, fn := range opts {
		fn(&ctx)
	}
	return ctx
}

// --------------------------------------------------------------------------------
// Posting Iterators (the core retrieval abstraction)
// --------------------------------------------------------------------------------

// Term names a (field, value) pair for debugging.
type Term struct {
	Field FieldName
	Value any
}

var WildcardTerm = NewTerm(WildcardFieldName, 0)

// PostingIterator is the minimal cursor abstraction for a single posting list.
// Both in-memory (SliceIterator) and mmap (flatPostingCursor) implement this.
type PostingIterator interface {
	Current() EntryID
	SkipTo(target EntryID) EntryID
	Term() Term
	// ReachEnd reports whether the iterator has exhausted its posting list.
	ReachEnd() bool
}

// NewTerm creates a Term for a field/value pair.
func NewTerm(field FieldName, v interface{}) Term {
	return Term{Field: field, Value: v}
}

// String returns a debug representation of the term.
func (key *Term) String() string {
	switch v := key.Value.(type) {
	case string:
		return fmt.Sprintf("[%s,%s]", key.Field, v)
	case int8, int16, int, int32, int64, uint8, uint16, uint, uint32, uint64:
		return fmt.Sprintf("[%s,%d]", key.Field, v)
	default:
		fmt.Println("unknown type", reflect.TypeOf(key.Value).String())
	}
	return fmt.Sprintf("[%s,%+v]", key.Field, key.Value)
}
