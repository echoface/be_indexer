// Package core defines the foundational types for the be_indexer.
// It contains type definitions and interfaces only — no business logic.
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
	WildcardFieldName = BEField("_Z_")

	IndexNameDefault     = "default"
	IndexNameACMatcher   = "ac_matcher"
	IndexNameExtendRange = "ext_range"
)

var (
	ErrFieldNotConfigured     = errors.New("field not configured")
	ErrFieldContainerRequired = errors.New("field container required")
	ErrUnknownContainer       = errors.New("unknown field container")
	ErrUnsupportedPredicate   = errors.New("unsupported predicate")
	ErrUnknownQueryField      = errors.New("unknown query field")
	ErrFieldIndexMissing      = errors.New("field index missing")
)

// --------------------------------------------------------------------------------
// Field Configuration
// --------------------------------------------------------------------------------

// FieldOption specifies how a field should be indexed.
type FieldOption struct {
	IndexType string // index kind: "default", "ac_matcher", "ext_range"
	Encoder   string // predicate encoder name (empty = use IndexType)
}

// IndexerSettings holds per-field configuration for an index.
type IndexerSettings struct {
	FieldConfig map[BEField]FieldOption
}

// FieldMeta combines field identity with its indexing options.
type FieldMeta struct {
	FieldOption
	ID    uint64
	Field BEField
}

// RangeRecord is the build-side interval record for ext_range containers.
type RangeRecord struct {
	Lo, Hi int64
}

// --------------------------------------------------------------------------------
// Result Collection
// --------------------------------------------------------------------------------

// ResultCollector receives matched documents during retrieval.
type ResultCollector interface {
	Add(id DocID, conj ConjID)
}

// RetrieveObserver is an optional observer for retrieval instrumentation.
// All methods are called synchronously on the retrieval goroutine.
type RetrieveObserver interface {
	OnRetrieveStart(ctx *RetrieveContext)
	OnRetrieveEnd(ctx *RetrieveContext)
	OnMatch(docID DocID, conjID ConjID)
	OnExcludeSkip(docID DocID)
	OnCursorInit(fieldCount int)
}

// FieldErrorObserver is an OPTIONAL extension of RetrieveObserver. When an
// observer also implements it, the engine reports per-field query errors
// (encoder or container lookup failures) through OnFieldError so callers can
// distinguish a genuine no-match from a skipped error even in lenient mode.
// Existing observers that do not implement it keep working unchanged.
type FieldErrorObserver interface {
	OnFieldError(field BEField, err error)
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
	Collector   ResultCollector
	Assigns     Assignments
	Observer    RetrieveObserver
}

// IndexOpt is a functional option for RetrieveContext.
type IndexOpt func(ctx *RetrieveContext)

// NewRetrieveCtx creates a context with the given assignments and options.
func NewRetrieveCtx(ass Assignments, opts ...IndexOpt) RetrieveContext {
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
	Field BEField
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
func NewTerm(field BEField, v interface{}) Term {
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

// --------------------------------------------------------------------------------
// Logger
// --------------------------------------------------------------------------------

// BEIndexLogger is the logging interface for the indexer.
type BEIndexLogger interface {
	Debugf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Errorf(format string, v ...interface{})
}
