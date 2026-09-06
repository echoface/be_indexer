package builder

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/manifest"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

const (
	defaultManifestVersion = 1
	defaultPostingEncoding = "conjid64-entryid64-v1"
)

// ErrGenerationExists is returned by NewFullIndexBuilder / NewDeltaIndexBuilder
// when the target generation directory already exists. Generations are
// immutable and never overwritten; the caller must remove the old generation
// or choose a new one.
var ErrGenerationExists = fmt.Errorf("target generation directory already exists")

// ErrBuilderClosed is returned by AddDocument/AddMutation/Build after the
// builder has been closed, poisoned by a fatal error, or already built.
var ErrBuilderClosed = fmt.Errorf("index builder is closed")

// ErrTooManyMutations is returned by DeltaIndexBuilder.AddMutation when the
// accumulated mutation count exceeds the configured MaxMutations limit.
var ErrTooManyMutations = fmt.Errorf("delta mutation count exceeds MaxMutations")

// BuildDirectoryOptions controls on-disk full/delta artifact generation.
type BuildDirectoryOptions struct {
	// MaxDocsPerSegment limits how many docs are built into one segment.
	// <= 0 means "no limit".
	MaxDocsPerSegment int
	// MaxPostingsInMemory limits flattened posting records held by the external
	// streaming segment builder before spilling a sorted run to disk.
	// <= 0 uses the segment package default.
	MaxPostingsInMemory int
	// MaxWildcardEntriesInMemory limits K=0 wildcard entries held before spilling
	// sorted sidecar runs during streaming full builds. <= 0 uses a default.
	MaxWildcardEntriesInMemory int
	// BuilderVersion is copied into SnapshotManifestRequest by callers that want
	// to expose the offline builder version in manifest metadata.
	BuilderVersion string
	// IgnoreUnindexedFields tolerates documents referencing fields absent from
	// the schema. Default (false) treats such a document as a doc-level error
	// (skipped under FailSkip, fatal under FailFast). See NormalizeOptions.
	IgnoreUnindexedFields bool
}

func (o BuildDirectoryOptions) normalizeOptions() NormalizeOptions {
	return NormalizeOptions{IgnoreUnindexedFields: o.IgnoreUnindexedFields}
}

// BuildFailMode controls how the builder reacts to per-document / per-mutation
// (isolatable) validation errors. Builder-level errors (disk IO, spill, merge,
// rename) always fail fast regardless of this mode.
type BuildFailMode int

const (
	// FailFast poisons the builder on any error; subsequent Add*/Build calls
	// are rejected. This is the default and the safe choice for indexes where a
	// missing document means incorrect serving results.
	FailFast BuildFailMode = iota
	// FailSkip skips an offending document/mutation (invoking the OnSkip
	// callback if set) and continues. Builder-level errors still fail fast.
	FailSkip
)

// FullIndexBuildOption configures a FullIndexBuilder.
type FullIndexBuildOption struct {
	Root              string
	Generation        uint64
	SnapshotWatermark uint64
	Fields            core.Schema
	Options           BuildDirectoryOptions
	FailMode          BuildFailMode
	// OnSkip is invoked for each document skipped under FailSkip. Optional.
	OnSkip func(docID core.DocID, err error)
}

// DeltaIndexBuildOption configures a DeltaIndexBuilder.
type DeltaIndexBuildOption struct {
	Root                   string
	Generation             uint64
	FromWatermarkExclusive uint64
	ToWatermarkInclusive   uint64
	Fields                 core.Schema
	Options                BuildDirectoryOptions
	FailMode               BuildFailMode
	// MaxMutations caps accumulated mutations. 0 means unlimited (default).
	MaxMutations int
	// OnSkip is invoked for each mutation skipped under FailSkip. Optional.
	OnSkip func(docID core.DocID, err error)
}

// SnapshotManifestRequest describes a complete publishable snapshot manifest.
type SnapshotManifestRequest struct {
	IndexName       string
	Generation      uint64
	Fields          core.Schema
	FormatVersion   string
	PostingEncoding string
	BuilderVersion  string
	Full            manifest.FullIndexDescriptor
	Deltas          []manifest.DeltaIndexDescriptor
}

// builderState tracks the lifecycle of a push builder.
type builderState int

const (
	stateAccepting builderState = iota // accepting Add*, not yet built
	statePoisoned                      // a fatal error occurred; reject Add*/Build
	stateBuilt                         // Build succeeded; terminal
	stateClosed                        // Close called before Build; tmp removed
)

// --------------------------------------------------------------------------------
// FullIndexBuilder — push model, streaming full index directory build.
// --------------------------------------------------------------------------------

// FullIndexBuilder builds a full index generation directory by accepting
// documents one at a time (push model). Documents are streamed into an
// external-sort segment builder so neither the corpus nor the postings need to
// be materialized in memory. Internal segment rolling (MaxDocsPerSegment) is an
// implementation detail invisible to callers.
//
// Lifecycle: New -> AddDocument* -> Build (commit) ; Close for cleanup/abort.
// The builder is NOT safe for concurrent use: AddDocument must be called
// serially. Build's success boundary is Rename(tmp -> final); it produces a
// self-describing, checksummed, immutable Descriptor. It never publishes or
// distributes — that is business infrastructure outside this library.
type FullIndexBuilder struct {
	opt      FullIndexBuildOption
	codec    *parser.SchemaCodec
	relPath  string
	finalDir string
	tmpDir   string

	maxDocsPerSegment int

	// current segment build state
	segIdx    int
	curSeg    *segmentBuild
	segments  []manifest.SegmentDescriptor
	docCount  int
	skipped   int
	state     builderState
	fatalErr  error
	closeOnce bool

	// seenDocs enforces global DocID uniqueness across the whole full build
	// (§4.1.5). Duplicate DocIDs would collide in ConjID space and merge
	// predicates from different versions into a phantom conjunction. roaring64
	// keeps this compact even across a 2^43 DocID space and many documents.
	seenDocs *roaring64.Bitmap
}

// segmentBuild holds the per-segment external builder + wildcard accumulator.
// Each segment owns an independent wildcard sidecar lifecycle.
type segmentBuild struct {
	tmpFile  *os.File
	tmpName  string
	esb      *segment.ExternalBuilder
	wildAcc  *entryRunAccumulator
	docCount int
	minDoc   core.DocID
	maxDoc   core.DocID
}

// NewFullIndexBuilder creates a full index builder. It fails fast if the target
// generation directory already exists (immutable generations, ErrGenerationExists),
// so a whole build is never wasted on a name collision.
func NewFullIndexBuilder(opt FullIndexBuildOption) (*FullIndexBuilder, error) {
	if opt.Root == "" {
		return nil, fmt.Errorf("root is required")
	}
	if opt.Generation == 0 {
		return nil, fmt.Errorf("generation is required")
	}
	if len(opt.Fields) == 0 {
		return nil, fmt.Errorf("fields are required")
	}
	codec, err := parser.NewSchemaCodec(opt.Fields)
	if err != nil {
		return nil, err
	}
	if err := validateCodecContainers(codec); err != nil {
		return nil, err
	}

	relPath := filepath.ToSlash(filepath.Join("full", generationDir("full", opt.Generation)))
	finalDir := filepath.Join(opt.Root, relPath)
	if err := ensureGenerationAbsent(finalDir); err != nil {
		return nil, err
	}
	tmpDir, err := makeBuildTmpDir(opt.Root, "building-full", opt.Generation)
	if err != nil {
		return nil, err
	}

	maxDocs := opt.Options.MaxDocsPerSegment
	if maxDocs <= 0 {
		maxDocs = int(^uint(0) >> 1)
	}

	return &FullIndexBuilder{
		opt:               opt,
		codec:             codec,
		relPath:           relPath,
		finalDir:          finalDir,
		tmpDir:            tmpDir,
		maxDocsPerSegment: maxDocs,
		state:             stateAccepting,
		seenDocs:          roaring64.New(),
	}, nil
}

// AddDocument streams one document into the current segment, rolling to a new
// segment when MaxDocsPerSegment is reached. Under FailFast a doc-level error
// poisons the builder; under FailSkip it is skipped (OnSkip invoked) and nil is
// returned. Builder-level errors always poison and return the error.
func (b *FullIndexBuilder) AddDocument(doc *core.Document) error {
	if b.state != stateAccepting {
		return ErrBuilderClosed
	}
	if doc == nil {
		return b.handleDocError(0, fmt.Errorf("nil document"))
	}

	// Encode the whole document into a document-local buffer first. Validation,
	// normalization and per-predicate encoding are all doc-level (isolatable)
	// errors: nothing is written to the segment sink until the document fully
	// encodes, so a failure here can be skipped under FailSkip without leaving a
	// half-written document (§4.1.4).
	encoded, err := encodeDocument(b.codec, doc, b.opt.Options.normalizeOptions())
	if err != nil {
		return b.handleDocError(doc.ID, err) // doc-level
	}

	// DocID uniqueness is a doc-level policy: a duplicate is the offending
	// document, not a builder failure, so FailSkip may skip it (§4.1.5).
	if b.seenDocs.Contains(docIDKey(doc.ID)) {
		return b.handleDocError(doc.ID, fmt.Errorf("duplicate doc id %d in full build", doc.ID))
	}

	if b.curSeg == nil {
		if err := b.startNewSegment(); err != nil {
			return b.fatal(err) // builder-level
		}
	}

	// Committing the pre-encoded records to the sink is builder-level: the
	// document already validated, so a failure means the sink/spill IO failed.
	if err := commitEncodedDoc(b.curSeg.esb, encoded); err != nil {
		return b.fatal(err) // builder-level
	}
	if err := b.curSeg.wildAcc.Add(encoded.wildcards); err != nil {
		return b.fatal(err) // builder-level (spill IO)
	}
	b.seenDocs.Add(docIDKey(doc.ID))

	if b.curSeg.docCount == 0 {
		b.curSeg.minDoc, b.curSeg.maxDoc = doc.ID, doc.ID
	} else {
		if doc.ID < b.curSeg.minDoc {
			b.curSeg.minDoc = doc.ID
		}
		if doc.ID > b.curSeg.maxDoc {
			b.curSeg.maxDoc = doc.ID
		}
	}
	b.curSeg.docCount++
	b.docCount++

	if b.curSeg.docCount >= b.maxDocsPerSegment {
		if err := b.finishCurrentSegment(); err != nil {
			return b.fatal(err)
		}
	}
	return nil
}

// docIDKey maps a signed DocID into the uint64 key space of the roaring64
// seen-set. The mapping only needs to be injective; a plain bit-reinterpret of
// the two's-complement value is stable and collision-free across the valid
// [-2^43, 2^43] DocID range.
func docIDKey(id core.DocID) uint64 { return uint64(id) }

// Build finalizes the current segment, commits the directory via rename, and
// returns a self-describing descriptor. On any failure it removes the tmp dir
// so the local filesystem returns to its pre-build state, and poisons the
// builder. Empty full builds are rejected.
func (b *FullIndexBuilder) Build() (manifest.FullIndexDescriptor, error) {
	if b.state != stateAccepting {
		if b.state == statePoisoned {
			return manifest.FullIndexDescriptor{}, b.fatalErr
		}
		return manifest.FullIndexDescriptor{}, ErrBuilderClosed
	}

	if b.curSeg != nil {
		if err := b.finishCurrentSegment(); err != nil {
			return manifest.FullIndexDescriptor{}, b.fatal(err)
		}
	}

	if len(b.segments) == 0 {
		return manifest.FullIndexDescriptor{}, b.fatal(fmt.Errorf("full documents are required"))
	}

	if err := publishBuiltDir(b.tmpDir, b.finalDir); err != nil {
		return manifest.FullIndexDescriptor{}, b.fatal(err)
	}
	b.state = stateBuilt

	return manifest.FullIndexDescriptor{
		Generation:        b.opt.Generation,
		SnapshotWatermark: b.opt.SnapshotWatermark,
		Path:              b.relPath,
		Segments:          b.segments,
	}, nil
}

// Close is idempotent. Before a successful Build it aborts and removes the tmp
// directory (restoring pre-build state); after Build it is a no-op. defer Close()
// is always safe.
func (b *FullIndexBuilder) Close() error {
	if b.closeOnce {
		return nil
	}
	b.closeOnce = true
	if b.curSeg != nil {
		b.curSeg.abort()
		b.curSeg = nil
	}
	if b.state == stateBuilt {
		return nil // tmp already consumed by rename
	}
	if b.state == stateAccepting {
		b.state = stateClosed
	}
	return os.RemoveAll(b.tmpDir)
}

// SkippedCount returns the number of documents skipped under FailSkip.
func (b *FullIndexBuilder) SkippedCount() int { return b.skipped }

func (b *FullIndexBuilder) handleDocError(docID core.DocID, err error) error {
	if b.opt.FailMode == FailSkip {
		b.skipped++
		if b.opt.OnSkip != nil {
			b.opt.OnSkip(docID, err)
		}
		return nil
	}
	return b.fatal(err)
}

// fatal poisons the builder, cleans up the current segment and the tmp dir, and
// returns the error. This makes "no successful build => restored state" the
// default behavior rather than relying on the caller's defer Close().
func (b *FullIndexBuilder) fatal(err error) error {
	b.state = statePoisoned
	b.fatalErr = err
	if b.curSeg != nil {
		b.curSeg.abort()
		b.curSeg = nil
	}
	_ = os.RemoveAll(b.tmpDir)
	return err
}

func (b *FullIndexBuilder) startNewSegment() error {
	if err := os.MkdirAll(b.tmpDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.tmpDir, ".segment-*.tmp")
	if err != nil {
		return err
	}
	wildAcc := newEntryRunAccumulator(
		filepath.Join(b.tmpDir, ".wildcard-runs", fmt.Sprintf("segment-%06d", b.segIdx)),
		b.opt.Options.MaxWildcardEntriesInMemory,
	)
	esb := segment.NewExternalBuilder(tmp, filepath.Join(b.tmpDir, ".runs", fmt.Sprintf("segment-%06d", b.segIdx)), segment.ExternalBuilderOptions{
		MaxPostingsInMemory: b.opt.Options.MaxPostingsInMemory,
	})
	for _, fc := range b.codec.Fields() {
		if err := esb.AddField(fc.Field, fc.Option); err != nil {
			seg := &segmentBuild{tmpFile: tmp, tmpName: tmp.Name(), esb: esb, wildAcc: wildAcc}
			seg.abort()
			return err
		}
	}
	b.curSeg = &segmentBuild{
		tmpFile: tmp,
		tmpName: tmp.Name(),
		esb:     esb,
		wildAcc: wildAcc,
	}
	return nil
}

// finishCurrentSegment writes the current segment's wildcard sidecar, seals the
// segment file, renames it into the tmp dir, records its descriptor, and resets
// for the next segment. Mirrors the tail of the previous pull-based
// buildStreamingSegmentToDir so push and pull produce byte-identical segments.
func (b *FullIndexBuilder) finishCurrentSegment() error {
	seg := b.curSeg
	if seg == nil {
		return nil
	}
	// Reset immediately so any error path won't double-abort via Close.
	b.curSeg = nil

	if seg.docCount == 0 {
		seg.abort()
		return nil
	}

	file := fmt.Sprintf("segment-%06d.bei", b.segIdx)
	path := filepath.Join(b.tmpDir, file)

	seg.esb.SetDocCount(seg.docCount)
	wildFile, _, err := seg.wildAcc.WriteSidecar(b.tmpDir, fmt.Sprintf(".segment-%06d-wildcards.bin", b.segIdx))
	if err != nil {
		seg.abort()
		return err
	}
	wildPath := filepath.Join(b.tmpDir, wildFile)
	seg.esb.SetWildcardsBlockFile(wildPath)

	if err := seg.esb.Write(); err != nil {
		_ = os.Remove(wildPath)
		seg.abort()
		return err
	}
	if err := seg.tmpFile.Sync(); err != nil {
		_ = os.Remove(wildPath)
		seg.abort()
		return err
	}
	if err := seg.tmpFile.Close(); err != nil {
		_ = os.Remove(wildPath)
		seg.wildAcc.Cleanup()
		return err
	}
	_ = os.Remove(wildPath)
	seg.wildAcc.Cleanup()

	if err := os.Rename(seg.tmpName, path); err != nil {
		return err
	}
	size, checksum, err := manifest.SHA256File(path)
	if err != nil {
		return err
	}
	b.segments = append(b.segments, manifest.SegmentDescriptor{
		SegmentID: uint32(b.segIdx),
		File:      file,
		Size:      size,
		Checksum:  checksum,
		DocCount:  uint64(seg.docCount),
		MinDocID:  int64(seg.minDoc),
		MaxDocID:  int64(seg.maxDoc),
	})
	b.segIdx++
	return nil
}

// abort discards a partially-built segment: closes and removes its tmp file and
// cleans up wildcard run files. Safe to call on a segment that never wrote.
func (s *segmentBuild) abort() {
	if s.tmpFile != nil {
		_ = s.tmpFile.Close()
		_ = os.Remove(s.tmpName)
		s.tmpFile = nil
	}
	if s.wildAcc != nil {
		s.wildAcc.Cleanup()
	}
}

// --------------------------------------------------------------------------------
// DeltaIndexBuilder — push model, delta index directory build.
// --------------------------------------------------------------------------------

// DeltaIndexBuilder builds a delta index generation directory by accepting
// mutations one at a time. Unlike the full builder, mutations are accumulated
// in memory until Build, because delta plan compaction (BuildDeltaPlan) must see
// all mutations to pick the highest version per DocID. Delta volumes are small
// by nature (a time-window increment), so this is an accepted trade-off; use a
// full rebuild for very large changes (MaxMutations guards against misuse).
type DeltaIndexBuilder struct {
	opt       DeltaIndexBuildOption
	relPath   string
	finalDir  string
	mutations []Mutation
	skipped   int
	state     builderState
	fatalErr  error
	closeOnce bool
}

// NewDeltaIndexBuilder creates a delta index builder, failing fast on an
// existing generation directory (ErrGenerationExists).
func NewDeltaIndexBuilder(opt DeltaIndexBuildOption) (*DeltaIndexBuilder, error) {
	if opt.Root == "" {
		return nil, fmt.Errorf("root is required")
	}
	if opt.Generation == 0 {
		return nil, fmt.Errorf("generation is required")
	}
	if opt.ToWatermarkInclusive <= opt.FromWatermarkExclusive {
		return nil, fmt.Errorf("invalid delta watermark range")
	}
	if len(opt.Fields) == 0 {
		return nil, fmt.Errorf("fields are required")
	}
	codec, err := parser.NewSchemaCodec(opt.Fields)
	if err != nil {
		return nil, err
	}
	if err := validateCodecContainers(codec); err != nil {
		return nil, err
	}
	relPath := filepath.ToSlash(filepath.Join("delta", generationDir("delta", opt.Generation)))
	finalDir := filepath.Join(opt.Root, relPath)
	if err := ensureGenerationAbsent(finalDir); err != nil {
		return nil, err
	}
	return &DeltaIndexBuilder{
		opt:      opt,
		relPath:  relPath,
		finalDir: finalDir,
		state:    stateAccepting,
	}, nil
}

// AddMutation accumulates one mutation. Only enqueue validation happens here;
// dedup/plan generation is deferred to Build. Under FailFast a validation error
// poisons the builder; under FailSkip it is skipped (OnSkip invoked).
func (b *DeltaIndexBuilder) AddMutation(m Mutation) error {
	if b.state != stateAccepting {
		return ErrBuilderClosed
	}
	if err := validateMutation(m); err != nil {
		if b.opt.FailMode == FailSkip {
			b.skipped++
			if b.opt.OnSkip != nil {
				b.opt.OnSkip(m.DocID, err)
			}
			return nil
		}
		b.state = statePoisoned
		b.fatalErr = err
		return err
	}
	if b.opt.MaxMutations > 0 && len(b.mutations) >= b.opt.MaxMutations {
		b.state = statePoisoned
		b.fatalErr = ErrTooManyMutations
		return ErrTooManyMutations
	}
	b.mutations = append(b.mutations, m)
	return nil
}

// Build compacts mutations into a delta plan, exports upsert documents into
// segments, writes changed/deleted sidecars, and commits via rename. An empty
// delta (no mutations) is valid and produces empty sidecars. On failure the tmp
// dir is removed to restore pre-build state.
func (b *DeltaIndexBuilder) Build() (manifest.DeltaIndexDescriptor, error) {
	if b.state != stateAccepting {
		if b.state == statePoisoned {
			return manifest.DeltaIndexDescriptor{}, b.fatalErr
		}
		return manifest.DeltaIndexDescriptor{}, ErrBuilderClosed
	}

	plan, err := BuildDeltaPlan(b.mutations)
	if err != nil {
		b.state = statePoisoned
		b.fatalErr = err
		return manifest.DeltaIndexDescriptor{}, err
	}

	tmpDir, err := makeBuildTmpDir(b.opt.Root, "building-delta", b.opt.Generation)
	if err != nil {
		b.state = statePoisoned
		b.fatalErr = err
		return manifest.DeltaIndexDescriptor{}, err
	}
	fail := func(e error) (manifest.DeltaIndexDescriptor, error) {
		b.state = statePoisoned
		b.fatalErr = e
		_ = os.RemoveAll(tmpDir)
		return manifest.DeltaIndexDescriptor{}, e
	}

	segments, err := buildSegmentsToDir(tmpDir, b.opt.Fields, plan.Documents, b.opt.Options)
	if err != nil {
		return fail(err)
	}
	changedFile, changedChecksum, err := manifest.WriteDocIDsSidecar(tmpDir, "changed_docs.bin", plan.ChangedDocs)
	if err != nil {
		return fail(err)
	}
	deletedFile, deletedChecksum, err := manifest.WriteDocIDsSidecar(tmpDir, "deleted_docs.bin", plan.DeletedDocs)
	if err != nil {
		return fail(err)
	}
	if err := publishBuiltDir(tmpDir, b.finalDir); err != nil {
		return fail(err)
	}
	b.state = stateBuilt

	return manifest.DeltaIndexDescriptor{
		Generation:             b.opt.Generation,
		FromWatermarkExclusive: b.opt.FromWatermarkExclusive,
		ToWatermarkInclusive:   b.opt.ToWatermarkInclusive,
		Path:                   b.relPath,
		Segments:               segments,
		ChangedDocsFile:        changedFile,
		ChangedDocsChecksum:    changedChecksum,
		DeletedDocsFile:        deletedFile,
		DeletedDocsChecksum:    deletedChecksum,
		ChangedDocCount:        uint64(len(plan.ChangedDocs)),
		DeletedDocCount:        uint64(len(plan.DeletedDocs)),
	}, nil
}

// Close is idempotent. Delta builders accumulate in memory and only touch disk
// inside Build (which cleans up its own tmp on failure), so Close mainly releases
// the accumulated mutations and marks the terminal state.
func (b *DeltaIndexBuilder) Close() error {
	if b.closeOnce {
		return nil
	}
	b.closeOnce = true
	b.mutations = nil
	if b.state == stateAccepting {
		b.state = stateClosed
	}
	return nil
}

// SkippedCount returns the number of mutations skipped under FailSkip.
func (b *DeltaIndexBuilder) SkippedCount() int { return b.skipped }

// --------------------------------------------------------------------------------
// Snapshot manifest (serving-side helper, unchanged).
// --------------------------------------------------------------------------------

// NewSnapshotManifest creates and validates a serving snapshot manifest.
func NewSnapshotManifest(req SnapshotManifestRequest) (manifest.Manifest, error) {
	formatVersion := req.FormatVersion
	if formatVersion == "" {
		formatVersion = manifest.FormatVersionSegmentV5
	}
	postingEncoding := req.PostingEncoding
	if postingEncoding == "" {
		postingEncoding = defaultPostingEncoding
	}
	codec, err := parser.NewSchemaCodec(req.Fields)
	if err != nil {
		return manifest.Manifest{}, err
	}
	m := manifest.Manifest{
		ManifestVersion: defaultManifestVersion,
		IndexName:       req.IndexName,
		Generation:      req.Generation,
		SchemaHash:      codec.SchemaHash(),
		FormatVersion:   formatVersion,
		PostingEncoding: postingEncoding,
		BuilderVersion:  req.BuilderVersion,
		Full:            req.Full,
		Deltas:          req.Deltas,
	}
	if err := m.Validate(); err != nil {
		return manifest.Manifest{}, err
	}
	return m, nil
}

// --------------------------------------------------------------------------------
// Internal helpers (shared by delta build path and tests).
// --------------------------------------------------------------------------------

func buildSegmentsToDir(dir string, fields core.Schema, docs []*core.Document, opts BuildDirectoryOptions) ([]manifest.SegmentDescriptor, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	codec, err := parser.NewSchemaCodec(fields)
	if err != nil {
		return nil, err
	}
	if err := validateUniqueDocIDs(docs); err != nil {
		return nil, err
	}
	maxDocsPerSegment := opts.MaxDocsPerSegment
	if maxDocsPerSegment <= 0 || maxDocsPerSegment > len(docs) {
		maxDocsPerSegment = len(docs)
	}

	segments := make([]manifest.SegmentDescriptor, 0, (len(docs)+maxDocsPerSegment-1)/maxDocsPerSegment)
	segIdx := 0
	for start := 0; start < len(docs); start += maxDocsPerSegment {
		end := start + maxDocsPerSegment
		if end > len(docs) {
			end = len(docs)
		}
		chunk := docs[start:end]
		buf := new(bytes.Buffer)
		err := writeSegmentFromDocsWithCodec(buf, codec, chunk, BuildSegmentFromDocsOptions{
			IgnoreUnindexedFields: opts.IgnoreUnindexedFields,
		})
		if err != nil {
			return nil, err
		}
		file := fmt.Sprintf("segment-%06d.bei", segIdx)
		data := buf.Bytes()
		if err := manifest.AtomicWriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return nil, err
		}
		minDoc, maxDoc := docRange(chunk)
		segments = append(segments, manifest.SegmentDescriptor{
			SegmentID: uint32(segIdx),
			File:      file,
			Size:      uint64(len(data)),
			Checksum:  manifest.SHA256Checksum(data),
			DocCount:  uint64(len(chunk)),
			MinDocID:  int64(minDoc),
			MaxDocID:  int64(maxDoc),
		})
		segIdx++
	}
	return segments, nil
}

func docRange(docs []*core.Document) (core.DocID, core.DocID) {
	minDoc := docs[0].ID
	maxDoc := docs[0].ID
	for _, doc := range docs[1:] {
		if doc.ID < minDoc {
			minDoc = doc.ID
		}
		if doc.ID > maxDoc {
			maxDoc = doc.ID
		}
	}
	return minDoc, maxDoc
}

func generationDir(prefix string, generation uint64) string {
	return fmt.Sprintf("%s-%06d", prefix, generation)
}

func makeBuildTmpDir(root, prefix string, generation uint64) (string, error) {
	tmpRoot := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(tmpRoot, fmt.Sprintf("%s-%06d-*", prefix, generation))
}

// ensureGenerationAbsent returns ErrGenerationExists if finalDir already exists.
func ensureGenerationAbsent(finalDir string) error {
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("%w: %s", ErrGenerationExists, finalDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func publishBuiltDir(tmpDir, finalDir string) error {
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("%w: %s", ErrGenerationExists, finalDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpDir, finalDir)
}
