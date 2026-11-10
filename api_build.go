package be_indexer

import (
	"io"

	"github.com/echoface/be_indexer/builder"
)

type (
	BuildSegmentOptions    = builder.BuildSegmentFromDocsOptions
	BuildSegmentsOptions   = builder.BuildSegmentsFromDocsOptions
	BuildDirectoryOptions  = builder.BuildDirectoryOptions
	FullIndexBuildOptions  = builder.FullIndexBuildOption
	DeltaIndexBuildOptions = builder.DeltaIndexBuildOption
	FullIndexBuilder       = builder.FullIndexBuilder
	DeltaIndexBuilder      = builder.DeltaIndexBuilder
	BuildFailMode          = builder.BuildFailMode
	MutationOp             = builder.MutationOp
	Mutation               = builder.Mutation
	DeltaPlan              = builder.DeltaPlan
)

const (
	FailFast       = builder.FailFast
	FailSkip       = builder.FailSkip
	MutationUpsert = builder.MutationUpsert
	MutationDelete = builder.MutationDelete
)

var (
	ErrGenerationExists = builder.ErrGenerationExists
	ErrBuilderClosed    = builder.ErrBuilderClosed
	ErrTooManyMutations = builder.ErrTooManyMutations
)

// BuildSegment compiles a document corpus into one Segment v5. The caller owns w.
func BuildSegment(w io.Writer, fields Schema, docs []*Document, opts BuildSegmentOptions) error {
	return builder.BuildSegmentFromDocs(w, fields, docs, opts)
}

// BuildSegments validates global DocID uniqueness and splits documents into segments.
// The caller owns each writer. See builder.BuildSegmentsFromDocs for error/count semantics.
func BuildSegments(newWriter func(int) (io.Writer, error), fields Schema, docs []*Document, opts BuildSegmentsOptions) (int, error) {
	return builder.BuildSegmentsFromDocs(newWriter, fields, docs, opts)
}

// NewFullIndexBuilder starts a streaming full build. Always defer Close.
func NewFullIndexBuilder(opts FullIndexBuildOptions) (*FullIndexBuilder, error) {
	return builder.NewFullIndexBuilder(opts)
}

// NewDeltaIndexBuilder starts a delta build. Always defer Close.
func NewDeltaIndexBuilder(opts DeltaIndexBuildOptions) (*DeltaIndexBuilder, error) {
	return builder.NewDeltaIndexBuilder(opts)
}

// BuildDeltaPlan keeps the latest version of each changed document.
func BuildDeltaPlan(mutations []Mutation) (*DeltaPlan, error) {
	return builder.BuildDeltaPlan(mutations)
}
