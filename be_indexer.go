// Package be_indexer is a high-performance Boolean Expression Indexing library
// based on the VLDB 09 paper "Indexing Boolean Expressions."
//
// It provides:
//   - Index building: convert DNF documents into memory-mappable binary segments
//   - Online retrieval: zero-copy mmap-based query evaluation via K-Groups algorithm
//   - Multi-segment: split large document sets across multiple segments
//
// Quick start:
//
//	// Build
//	buf := new(bytes.Buffer)
//	err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})
//	if err != nil { ... }
//
//	// Query
//	reader, _ := be_indexer.NewSegmentReader(buf.Bytes())
//	engine, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
//	results, _ := engine.Retrieve(be_indexer.Assignments{"age": 25})
//
// For multi-segment builds (large document sets):
//
//	count, err := be_indexer.BuildSegments(writerFn, fields, docs, be_indexer.BuildOptions{MaxDocsPerSegment: 100000})
package be_indexer

import (
	"io"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/compact"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
	"github.com/echoface/be_indexer/segment"
)

// --------------------------------------------------------------------------------
// Type Re-exports from core
// --------------------------------------------------------------------------------

type (
	DocID       = core.DocID
	DocIDList   = core.DocIDList
	BEField     = core.BEField
	Values      = core.Values
	ValueOpt    = core.ValueOpt
	Assignments = core.Assignments

	Document    = core.Document
	Conjunction = core.Conjunction
	Predicate   = core.Predicate
	ValueExpr   = core.ValueExpr

	ConjID  = core.ConjID
	EntryID = core.EntryID

	FieldMeta   = core.FieldMeta
	FieldOption = core.FieldOption

	LiveDocs       = core.LiveDocs
	DocIDCollector = core.DocIDCollector

	ResultCollector = core.ResultCollector
	RetrieveContext = core.RetrieveContext
	IndexOpt        = core.IndexOpt

	RetrieveObserver = core.RetrieveObserver

	Term            = core.Term
	PostingIterator = core.PostingIterator

	BEIndexLogger = core.BEIndexLogger

	BitmapDocSet    = core.BitmapDocSet
	IndexSnapshot   = engine.IndexSnapshot
	CompositeEngine = engine.CompositeEngine

	MutationOp = builder.MutationOp
	Mutation   = builder.Mutation
	DeltaPlan  = builder.DeltaPlan

	BuildDirectoryOptions   = builder.BuildDirectoryOptions
	BuildSegmentOptions     = builder.BuildSegmentFromDocsOptions
	BuildFailMode           = builder.BuildFailMode
	FullIndexBuilder        = builder.FullIndexBuilder
	DeltaIndexBuilder       = builder.DeltaIndexBuilder
	FullIndexBuildOption    = builder.FullIndexBuildOption
	DeltaIndexBuildOption   = builder.DeltaIndexBuildOption
	SnapshotManifestRequest = builder.SnapshotManifestRequest

	Manifest             = manifest.Manifest
	SegmentDescriptor    = manifest.SegmentDescriptor
	FullIndexDescriptor  = manifest.FullIndexDescriptor
	DeltaIndexDescriptor = manifest.DeltaIndexDescriptor

	LoaderOptions   = loader.Options
	IndexHolder     = loader.Holder
	SegmentLoadMode = loader.SegmentLoadMode

	CompactDecision           = compact.Decision
	CompactStats              = compact.Stats
	CompactPolicy             = compact.Policy
	CompactRuntimeObservation = compact.RuntimeObservation
	CompactOptions            = compact.Options
	CompactReason             = compact.Reason
	CompactRecommendation     = compact.Recommendation
)

// Re-exported constants and errors.
const (
	IndexNameDefault     = core.IndexNameDefault
	IndexNameACMatcher   = core.IndexNameACMatcher
	IndexNameExtendRange = core.IndexNameExtendRange
	SegmentVersionV4     = segment.SegmentVersionV4

	FormatVersionSegmentV4        = manifest.FormatVersionSegmentV4
	SegmentLoadHeapVerify         = loader.SegmentLoadHeapVerify
	SegmentLoadMmapVerify         = loader.SegmentLoadMmapVerify
	SegmentLoadMmapTrustPublished = loader.SegmentLoadMmapTrustPublished

	CompactDecisionNone  = compact.DecisionNone
	CompactDecisionMinor = compact.DecisionMinor
	CompactDecisionMajor = compact.DecisionMajor
)

var (
	ErrFieldNotConfigured   = core.ErrFieldNotConfigured
	ErrUnknownQueryField    = core.ErrUnknownQueryField
	ErrFieldIndexMissing    = core.ErrFieldIndexMissing
	ErrUnsupportedPredicate = core.ErrUnsupportedPredicate
)

// Factory functions from core.
var (
	NewDocument       = core.NewDocument
	NewConjunction    = core.NewConjunction
	NewLiveDocs       = core.NewLiveDocs
	NewDocIDCollector = core.NewDocIDCollector
	PickCollector     = core.PickCollector
	PutCollector      = core.PutCollector

	NewIntValues   = core.NewIntValues
	NewInt32Values = core.NewInt32Values
	NewInt64Values = core.NewInt64Values
	NewStrValues   = core.NewStrValues
)

// --------------------------------------------------------------------------------
// Engine Re-exports
// --------------------------------------------------------------------------------

// NewEngine creates a BooleanEngine from fields and immutable segments.
var NewEngine = engine.NewBooleanEngine

// NewCompositeEngine creates a full+delta CompositeEngine from an immutable snapshot.
var NewCompositeEngine = engine.NewCompositeEngine

// NewBitmapDocSet creates a compact DocID set used by CompositeEngine snapshots.
var NewBitmapDocSet = core.NewBitmapDocSet

// BuildDeltaPlan compacts mutation events by DocID and keeps the latest version.
var BuildDeltaPlan = builder.BuildDeltaPlan

// NewFullIndexBuilder creates a push-model full index directory builder.
var NewFullIndexBuilder = builder.NewFullIndexBuilder

// NewDeltaIndexBuilder creates a push-model delta index directory builder.
var NewDeltaIndexBuilder = builder.NewDeltaIndexBuilder

// NewSnapshotManifest creates a validated publishable snapshot manifest.
var NewSnapshotManifest = builder.NewSnapshotManifest

// OpenIndex loads an index_root/CURRENT manifest into a CompositeEngine.
var OpenIndex = loader.OpenIndex

// LoadSnapshot loads an index_root/CURRENT manifest into an immutable snapshot.
var LoadSnapshot = loader.LoadSnapshot

// NewIndexHolder loads and owns a reloadable index holder.
var NewIndexHolder = loader.NewHolder

// PublishManifest writes manifests/<name> and atomically switches CURRENT.
var PublishManifest = manifest.PublishManifest

// PublishCurrent atomically switches CURRENT to a manifest reference.
var PublishCurrent = manifest.PublishCurrent

// WriteDocIDsSidecar writes DocID sidecars and returns checksum metadata.
var WriteDocIDsSidecar = manifest.WriteDocIDsSidecar

// CollectCompactStats summarizes manifest-level compact statistics.
var CollectCompactStats = compact.CollectStats

// DefaultCompactPolicy returns default compact policy thresholds.
var DefaultCompactPolicy = compact.DefaultPolicy

// DecideCompact computes compact recommendation from a manifest.
var DecideCompact = compact.Decide

// DecideCompactStats computes compact recommendation from pre-collected stats.
var DecideCompactStats = compact.DecideStats

// Engine is the online query engine.
type Engine = engine.BooleanEngine

// --------------------------------------------------------------------------------
// Builder Re-exports
// --------------------------------------------------------------------------------

// BuildOptions controls segment splitting during build.
type BuildOptions = builder.BuildSegmentsFromDocsOptions

// BuildSegments builds one or more v4 segments and validates DocID uniqueness
// across the complete input corpus before creating any segment writer.
func BuildSegments(
	newWriter func(segIdx int) (io.Writer, error),
	fields map[BEField]*FieldMeta,
	docs []*Document,
	opt BuildOptions,
) (int, error) {
	return builder.BuildSegmentsFromDocs(newWriter, fields, docs, opt)
}

// BuildSegment builds a single Segment v4. Wildcard EntryIDs and block
// checksums are embedded in the segment.
func BuildSegment(w io.Writer, fields map[BEField]*FieldMeta, docs []*Document, opts BuildSegmentOptions) error {
	return builder.BuildSegmentFromDocs(w, fields, docs, opts)
}

// --------------------------------------------------------------------------------
// Segment Re-exports
// --------------------------------------------------------------------------------

// NewSegmentReader parses an in-memory segment byte slice for query.
var NewSegmentReader = segment.NewSegmentReader

// OpenSegmentFile memory-maps a segment file read-only for zero-copy serving.
// Call SegmentReader.Close to unmap when done.
var OpenSegmentFile = segment.OpenSegmentFile

// SegmentReader serves queries from a segment, backed by mmap or memory.
type SegmentReader = segment.SegmentReader

// --------------------------------------------------------------------------------
// Observability
// --------------------------------------------------------------------------------

// WithObserver returns an IndexOpt that attaches an observer to retrieval.
func WithObserver(obs RetrieveObserver) IndexOpt {
	return func(ctx *RetrieveContext) {
		ctx.Observer = obs
	}
}

// WithStrictQuery returns an IndexOpt that enables strict query error handling.
// In strict mode the first per-field query error (encoder or container lookup
// failure) aborts retrieval and is returned to the caller instead of being
// treated as a no-match. The default is lenient.
func WithStrictQuery() IndexOpt {
	return func(ctx *RetrieveContext) {
		ctx.StrictQuery = true
	}
}
