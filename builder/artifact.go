package builder

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/manifest"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

const (
	defaultManifestVersion = 1
	defaultPostingEncoding = "conjid64-entryid64-v1"
)

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
	// SegmentSchemaHash is embedded into every segment and should match the
	// snapshot manifest schema hash.
	SegmentSchemaHash string
}

// DocumentIterator streams full-build documents without requiring callers to
// materialize the entire corpus in memory.
type DocumentIterator interface {
	Next() (*core.Document, bool, error)
}

// DocumentIteratorFunc adapts a function to DocumentIterator.
type DocumentIteratorFunc func() (*core.Document, bool, error)

func (f DocumentIteratorFunc) Next() (*core.Document, bool, error) { return f() }

// FullBuildRequest describes a full index generation build.
type FullBuildRequest struct {
	Root              string
	Generation        uint64
	SnapshotWatermark uint64
	Fields            map[core.BEField]*core.FieldMeta
	Documents         []*core.Document
	Options           BuildDirectoryOptions
}

// FullStreamBuildRequest describes a streaming full index generation build.
type FullStreamBuildRequest struct {
	Root              string
	Generation        uint64
	SnapshotWatermark uint64
	Fields            map[core.BEField]*core.FieldMeta
	Documents         DocumentIterator
	Options           BuildDirectoryOptions
}

// DeltaBuildRequest describes a delta index generation build.
type DeltaBuildRequest struct {
	Root                   string
	Generation             uint64
	FromWatermarkExclusive uint64
	ToWatermarkInclusive   uint64
	Fields                 map[core.BEField]*core.FieldMeta
	Mutations              []Mutation
	Options                BuildDirectoryOptions
}

// SnapshotManifestRequest describes a complete publishable snapshot manifest.
type SnapshotManifestRequest struct {
	IndexName       string
	Generation      uint64
	SchemaHash      string
	FormatVersion   string
	PostingEncoding string
	BuilderVersion  string
	Full            manifest.FullIndexDescriptor
	Deltas          []manifest.DeltaIndexDescriptor
}

// BuildFullIndexDir builds all full index files under root/full/full-<generation>.
// Files are first created under root/tmp and then published by directory rename.
func BuildFullIndexDir(req FullBuildRequest) (manifest.FullIndexDescriptor, error) {
	return BuildFullIndexDirFromIterator(FullStreamBuildRequest{
		Root:              req.Root,
		Generation:        req.Generation,
		SnapshotWatermark: req.SnapshotWatermark,
		Fields:            req.Fields,
		Documents:         newSliceDocumentIterator(req.Documents),
		Options:           req.Options,
	})
}

// BuildFullIndexDirFromIterator builds full index files from a streaming document iterator.
// It uses an external-sort segment builder so neither the full document corpus nor
// all segment postings need to be materialized in memory.
func BuildFullIndexDirFromIterator(req FullStreamBuildRequest) (manifest.FullIndexDescriptor, error) {
	if req.Root == "" {
		return manifest.FullIndexDescriptor{}, fmt.Errorf("root is required")
	}
	if req.Generation == 0 {
		return manifest.FullIndexDescriptor{}, fmt.Errorf("generation is required")
	}
	if len(req.Fields) == 0 {
		return manifest.FullIndexDescriptor{}, fmt.Errorf("fields are required")
	}
	if req.Documents == nil {
		return manifest.FullIndexDescriptor{}, fmt.Errorf("document iterator is required")
	}

	relPath := filepath.ToSlash(filepath.Join("full", generationDir("full", req.Generation)))
	finalDir := filepath.Join(req.Root, relPath)
	tmpDir, err := makeBuildTmpDir(req.Root, "building-full", req.Generation)
	if err != nil {
		return manifest.FullIndexDescriptor{}, err
	}
	defer os.RemoveAll(tmpDir)

	segments, err := buildStreamingSegmentsToDir(tmpDir, req.Fields, req.Documents, req.Options)
	if err != nil {
		return manifest.FullIndexDescriptor{}, err
	}
	if len(segments) == 0 {
		return manifest.FullIndexDescriptor{}, fmt.Errorf("full documents are required")
	}
	if err := publishBuiltDir(tmpDir, finalDir); err != nil {
		return manifest.FullIndexDescriptor{}, err
	}

	return manifest.FullIndexDescriptor{
		Generation:        req.Generation,
		SnapshotWatermark: req.SnapshotWatermark,
		Path:              relPath,
		Segments:          segments,
	}, nil
}

type sliceDocumentIterator struct {
	docs []*core.Document
	idx  int
}

func newSliceDocumentIterator(docs []*core.Document) *sliceDocumentIterator {
	return &sliceDocumentIterator{docs: docs}
}

func (it *sliceDocumentIterator) Next() (*core.Document, bool, error) {
	if it.idx >= len(it.docs) {
		return nil, false, nil
	}
	doc := it.docs[it.idx]
	it.idx++
	return doc, true, nil
}

// BuildDeltaIndexDir builds delta segment and sidecar files under root/delta/delta-<generation>.
func BuildDeltaIndexDir(req DeltaBuildRequest) (manifest.DeltaIndexDescriptor, error) {
	if req.Root == "" {
		return manifest.DeltaIndexDescriptor{}, fmt.Errorf("root is required")
	}
	if req.Generation == 0 {
		return manifest.DeltaIndexDescriptor{}, fmt.Errorf("generation is required")
	}
	if req.ToWatermarkInclusive <= req.FromWatermarkExclusive {
		return manifest.DeltaIndexDescriptor{}, fmt.Errorf("invalid delta watermark range")
	}
	if len(req.Fields) == 0 {
		return manifest.DeltaIndexDescriptor{}, fmt.Errorf("fields are required")
	}

	plan, err := BuildDeltaPlan(req.Mutations)
	if err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}
	relPath := filepath.ToSlash(filepath.Join("delta", generationDir("delta", req.Generation)))
	finalDir := filepath.Join(req.Root, relPath)
	tmpDir, err := makeBuildTmpDir(req.Root, "building-delta", req.Generation)
	if err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}
	defer os.RemoveAll(tmpDir)

	segments, _, err := buildSegmentsToDir(tmpDir, req.Fields, plan.Documents, req.Options)
	if err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}
	changedFile, changedChecksum, err := manifest.WriteDocIDsSidecar(tmpDir, "changed_docs.bin", plan.ChangedDocs)
	if err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}
	deletedFile, deletedChecksum, err := manifest.WriteDocIDsSidecar(tmpDir, "deleted_docs.bin", plan.DeletedDocs)
	if err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}
	if err := publishBuiltDir(tmpDir, finalDir); err != nil {
		return manifest.DeltaIndexDescriptor{}, err
	}

	return manifest.DeltaIndexDescriptor{
		Generation:             req.Generation,
		FromWatermarkExclusive: req.FromWatermarkExclusive,
		ToWatermarkInclusive:   req.ToWatermarkInclusive,
		Path:                   relPath,
		Segments:               segments,
		ChangedDocsFile:        changedFile,
		ChangedDocsChecksum:    changedChecksum,
		DeletedDocsFile:        deletedFile,
		DeletedDocsChecksum:    deletedChecksum,
		ChangedDocCount:        uint64(len(plan.ChangedDocs)),
		DeletedDocCount:        uint64(len(plan.DeletedDocs)),
	}, nil
}

// NewSnapshotManifest creates and validates a serving snapshot manifest.
func NewSnapshotManifest(req SnapshotManifestRequest) (manifest.Manifest, error) {
	formatVersion := req.FormatVersion
	if formatVersion == "" {
		formatVersion = manifest.FormatVersionSegmentV2
	}
	postingEncoding := req.PostingEncoding
	if postingEncoding == "" {
		postingEncoding = defaultPostingEncoding
	}
	m := manifest.Manifest{
		ManifestVersion: defaultManifestVersion,
		IndexName:       req.IndexName,
		Generation:      req.Generation,
		SchemaHash:      req.SchemaHash,
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

func buildSegmentsToDir(dir string, fields map[core.BEField]*core.FieldMeta, docs []*core.Document, opts BuildDirectoryOptions) ([]manifest.SegmentDescriptor, core.Entries, error) {
	if len(docs) == 0 {
		return nil, nil, nil
	}
	codec, err := parser.NewSchemaCodec(fields)
	if err != nil {
		return nil, nil, err
	}
	maxDocsPerSegment := opts.MaxDocsPerSegment
	if maxDocsPerSegment <= 0 || maxDocsPerSegment > len(docs) {
		maxDocsPerSegment = len(docs)
	}

	segments := make([]manifest.SegmentDescriptor, 0, (len(docs)+maxDocsPerSegment-1)/maxDocsPerSegment)
	var allWildcards core.Entries
	segIdx := 0
	for start := 0; start < len(docs); start += maxDocsPerSegment {
		end := start + maxDocsPerSegment
		if end > len(docs) {
			end = len(docs)
		}
		chunk := docs[start:end]
		buf := new(bytes.Buffer)
		wildcards, err := buildSegmentFromDocsWithCodec(buf, codec, chunk, BuildSegmentFromDocsOptions{
			SchemaHash: opts.SegmentSchemaHash,
		})
		if err != nil {
			return nil, nil, err
		}
		file := fmt.Sprintf("segment-%06d.bei", segIdx)
		data := buf.Bytes()
		if err := manifest.AtomicWriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			return nil, nil, err
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
		allWildcards = append(allWildcards, wildcards...)
		segIdx++
	}
	sort.Slice(allWildcards, func(i, j int) bool { return allWildcards[i] < allWildcards[j] })
	return segments, allWildcards, nil
}

func buildStreamingSegmentsToDir(dir string, fields map[core.BEField]*core.FieldMeta, docs DocumentIterator, opts BuildDirectoryOptions) ([]manifest.SegmentDescriptor, error) {
	codec, err := parser.NewSchemaCodec(fields)
	if err != nil {
		return nil, err
	}
	maxDocsPerSegment := opts.MaxDocsPerSegment
	if maxDocsPerSegment <= 0 {
		maxDocsPerSegment = int(^uint(0) >> 1)
	}

	var segments []manifest.SegmentDescriptor
	segIdx := 0

	for {
		seg, ok, err := buildStreamingSegmentToDir(dir, codec, docs, segIdx, maxDocsPerSegment, opts)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		segments = append(segments, seg)
		segIdx++
	}
	return segments, nil
}

func buildStreamingSegmentToDir(
	dir string,
	codec *parser.SchemaCodec,
	docs DocumentIterator,
	segIdx int,
	maxDocsPerSegment int,
	opts BuildDirectoryOptions,
) (manifest.SegmentDescriptor, bool, error) {
	file := fmt.Sprintf("segment-%06d.bei", segIdx)
	path := filepath.Join(dir, file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return manifest.SegmentDescriptor{}, false, err
	}
	tmp, err := os.CreateTemp(dir, ".segment-*.tmp")
	if err != nil {
		return manifest.SegmentDescriptor{}, false, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	segmentWildAcc := newEntryRunAccumulator(filepath.Join(dir, ".wildcard-runs", fmt.Sprintf("segment-%06d", segIdx)), opts.MaxWildcardEntriesInMemory)
	defer segmentWildAcc.Cleanup()

	esb := segment.NewExternalBuilder(tmp, filepath.Join(dir, ".runs", fmt.Sprintf("segment-%06d", segIdx)), segment.ExternalBuilderOptions{
		MaxPostingsInMemory: opts.MaxPostingsInMemory,
		SchemaHash:          opts.SegmentSchemaHash,
	})
	for _, fc := range codec.Fields() {
		esb.AddField(fc.Meta)
	}

	var docCount int
	var minDoc, maxDoc core.DocID
	for docCount < maxDocsPerSegment {
		doc, ok, err := docs.Next()
		if err != nil {
			_ = tmp.Close()
			return manifest.SegmentDescriptor{}, false, err
		}
		if !ok {
			break
		}
		if doc == nil {
			_ = tmp.Close()
			return manifest.SegmentDescriptor{}, false, fmt.Errorf("document iterator returned nil document")
		}
		if docCount == 0 {
			minDoc, maxDoc = doc.ID, doc.ID
		} else {
			if doc.ID < minDoc {
				minDoc = doc.ID
			}
			if doc.ID > maxDoc {
				maxDoc = doc.ID
			}
		}
		w, err := exportDocToSink(esb, codec, doc)
		if err != nil {
			_ = tmp.Close()
			return manifest.SegmentDescriptor{}, false, err
		}
		if err := segmentWildAcc.Add(w); err != nil {
			_ = tmp.Close()
			return manifest.SegmentDescriptor{}, false, err
		}
		docCount++
	}
	if docCount == 0 {
		_ = tmp.Close()
		return manifest.SegmentDescriptor{}, false, nil
	}
	esb.SetDocCount(docCount)
	wildFile, _, err := segmentWildAcc.WriteSidecar(dir, fmt.Sprintf(".segment-%06d-wildcards.bin", segIdx))
	if err != nil {
		_ = tmp.Close()
		return manifest.SegmentDescriptor{}, false, err
	}
	wildPath := filepath.Join(dir, wildFile)
	defer os.Remove(wildPath)
	esb.SetWildcardsBlockFile(wildPath)
	if err := esb.Write(); err != nil {
		_ = tmp.Close()
		return manifest.SegmentDescriptor{}, false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return manifest.SegmentDescriptor{}, false, err
	}
	if err := tmp.Close(); err != nil {
		return manifest.SegmentDescriptor{}, false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return manifest.SegmentDescriptor{}, false, err
	}
	committed = true
	size, checksum, err := manifest.SHA256File(path)
	if err != nil {
		return manifest.SegmentDescriptor{}, false, err
	}
	return manifest.SegmentDescriptor{
		SegmentID: uint32(segIdx),
		File:      file,
		Size:      size,
		Checksum:  checksum,
		DocCount:  uint64(docCount),
		MinDocID:  int64(minDoc),
		MaxDocID:  int64(maxDoc),
	}, true, nil
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

func publishBuiltDir(tmpDir, finalDir string) error {
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("target build directory already exists: %s", finalDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(tmpDir, finalDir)
}
