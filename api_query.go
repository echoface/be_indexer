package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
)

type (
	Engine                     = engine.BooleanEngine
	ConjID                     = core.ConjID
	EntryID                    = core.EntryID
	Term                       = core.Term
	PostingIterator            = core.PostingIterator
	DocVisibilityMask          = core.DocVisibilityMask
	BitmapDocSet               = core.BitmapDocSet
	ResultCollector            = core.ResultCollector
	DuplicateTolerantCollector = core.DuplicateTolerantCollector
	CollectResult              = core.CollectResult
	DocIDCollector             = core.DocIDCollector
	RetrieveContext            = core.RetrieveContext
	QueryOption                = core.QueryOption
	RetrieveObserver           = core.RetrieveObserver
	FieldErrorObserver         = core.FieldErrorObserver
)

const (
	CollectContinue = core.CollectContinue
	CollectStop     = core.CollectStop
)

var (
	ErrFieldNotConfigured       = core.ErrFieldNotConfigured
	ErrUnknownQueryField        = core.ErrUnknownQueryField
	ErrFieldIndexMissing        = core.ErrFieldIndexMissing
	ErrUnsupportedPredicate     = core.ErrUnsupportedPredicate
	ErrUnknownContainer         = core.ErrUnknownContainer
	ErrNilResultCollector       = core.ErrNilResultCollector
	ErrDuplicateResultCollector = core.ErrDuplicateResultCollector
)

// NewEngine validates the schema and binds immutable segment readers. Close the
// engine only after its queries finish; it owns the readers after successful construction.
func NewEngine(fields Schema, segments []*SegmentReader) (*Engine, error) {
	return engine.NewBooleanEngine(fields, segments)
}

// NewDocVisibilityMask creates a visibility mask to configure before publishing an engine.
func NewDocVisibilityMask() *DocVisibilityMask { return core.NewDocVisibilityMask() }

// NewBitmapDocSet creates a mutable set of document IDs.
func NewBitmapDocSet(ids ...DocID) *BitmapDocSet { return core.NewBitmapDocSet(ids...) }

// NewDocIDCollector creates a reusable bitmap collector. Reset it between queries.
func NewDocIDCollector() *DocIDCollector { return core.NewDocIDCollector() }

// WithObserver attaches synchronous per-layer retrieval instrumentation.
func WithObserver(observer RetrieveObserver) QueryOption {
	return func(ctx *RetrieveContext) { ctx.Observer = observer }
}

// WithStrictQuery returns encoding/container errors instead of skipping the
// failing lookup. Lenient skipping can suppress includes or bypass exclusions.
func WithStrictQuery() QueryOption { return func(ctx *RetrieveContext) { ctx.StrictQuery = true } }

// WithScorePruning controls certified score skips; disabling them still gives exact Top K.
func WithScorePruning(enabled bool) QueryOption {
	return func(ctx *RetrieveContext) { ctx.DisableScorePruning = !enabled }
}
