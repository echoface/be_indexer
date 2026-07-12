package loader

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/manifest"
	"github.com/echoface/be_indexer/segment"
)

// Options controls OpenIndex validation.
type Options struct {
	// SchemaHash, when non-empty, must match Manifest.SchemaHash.
	SchemaHash string
	// VerifySegmentBlockChecksums controls whether every segment block checksum is
	// re-hashed while opening readers. The loader already verifies the whole file
	// through the manifest checksum, so false avoids a second O(segment_size) scan
	// on serving cold start while still validating checksum metadata shape.
	VerifySegmentBlockChecksums bool
	// UseMmap memory-maps segment files instead of reading them fully into the
	// heap. This shares the OS page cache and keeps RSS proportional to touched
	// pages, which is the recommended serving mode. Mmap intentionally skips the
	// whole-file manifest checksum (it would fault in every page); integrity then
	// relies on block checksum metadata, optionally strengthened by
	// VerifySegmentBlockChecksums.
	UseMmap bool
}

// OpenIndex loads index_root/CURRENT and returns a CompositeEngine snapshot.
func OpenIndex(root string, fields map[core.BEField]*core.FieldMeta, opts Options) (*engine.CompositeEngine, error) {
	snapshot, err := LoadSnapshot(root, fields, opts)
	if err != nil {
		return nil, err
	}
	return engine.NewCompositeEngine(snapshot), nil
}

// LoadSnapshot loads a manifest generation and all referenced segments/sidecars.
func LoadSnapshot(root string, fields map[core.BEField]*core.FieldMeta, opts Options) (*engine.IndexSnapshot, error) {
	m, err := ReadCurrentManifest(root)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if opts.SchemaHash != "" && opts.SchemaHash != m.SchemaHash {
		return nil, fmt.Errorf("schema hash mismatch: got %s, want %s", m.SchemaHash, opts.SchemaHash)
	}

	fullEngine, err := loadFullEngine(root, fields, m.SchemaHash, m.Full, opts)
	if err != nil {
		return nil, fmt.Errorf("load full: %w", err)
	}

	deltaEngines, changedDocs, deletedDocs, err := loadDeltas(root, fields, m.SchemaHash, m.Deltas, opts)
	if err != nil {
		return nil, fmt.Errorf("load deltas: %w", err)
	}

	return &engine.IndexSnapshot{
		Generation:   m.Generation,
		FullEngine:   fullEngine,
		DeltaEngines: deltaEngines,
		ChangedDocs:  changedDocs,
		DeletedDocs:  deletedDocs,
	}, nil
}

// ReadCurrentManifest reads CURRENT and the referenced manifest JSON.
func ReadCurrentManifest(root string) (manifest.Manifest, error) {
	currentBytes, err := os.ReadFile(filepath.Join(root, "CURRENT"))
	if err != nil {
		return manifest.Manifest{}, err
	}
	current := strings.TrimSpace(string(currentBytes))
	if current == "" {
		return manifest.Manifest{}, fmt.Errorf("CURRENT is empty")
	}
	manifestPath := current
	if !filepath.IsAbs(manifestPath) {
		if filepath.Dir(manifestPath) == "." {
			manifestPath = filepath.Join(root, "manifests", manifestPath)
		} else {
			manifestPath = filepath.Join(root, manifestPath)
		}
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest.Manifest{}, err
	}
	var m manifest.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest.Manifest{}, err
	}
	return m, nil
}

func loadFullEngine(root string, fields map[core.BEField]*core.FieldMeta, schemaHash string, full manifest.FullIndexDescriptor, opts Options) (*engine.BooleanEngine, error) {
	segments, err := loadSegments(root, full.Path, full.Segments, schemaHash, opts)
	if err != nil {
		return nil, err
	}
	return engine.NewBooleanEngine(fields, nil, segments)
}

func loadDeltas(root string, fields map[core.BEField]*core.FieldMeta, schemaHash string, deltas []manifest.DeltaIndexDescriptor, opts Options) ([]*engine.BooleanEngine, *core.BitmapDocSet, *core.BitmapDocSet, error) {
	changedDocs := core.NewBitmapDocSet()
	deletedDocs := core.NewBitmapDocSet()
	deltaEngines := make([]*engine.BooleanEngine, 0, len(deltas))
	laterChangedDocs := core.NewBitmapDocSet()
	for i := len(deltas) - 1; i >= 0; i-- {
		delta := deltas[i]
		changed, err := loadDocIDSidecar(root, delta.Path, delta.ChangedDocsFile, delta.ChangedDocsChecksum)
		if err != nil {
			return nil, nil, nil, err
		}
		segments, err := loadSegments(root, delta.Path, delta.Segments, schemaHash, opts)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(segments) > 0 {
			deltaEngine, err := engine.NewBooleanEngine(fields, nil, segments)
			if err != nil {
				return nil, nil, nil, err
			}
			if laterChangedDocs.Cardinality() > 0 {
				ld := core.NewLiveDocs()
				laterChangedDocs.ForEach(func(id core.DocID) { ld.MarkDeleted(id) })
				deltaEngine.SetLiveDocs(ld)
			}
			deltaEngines = append([]*engine.BooleanEngine{deltaEngine}, deltaEngines...)
		}
		for _, id := range changed {
			laterChangedDocs.Add(id)
		}
	}

	for _, delta := range deltas {
		changed, err := loadDocIDSidecar(root, delta.Path, delta.ChangedDocsFile, delta.ChangedDocsChecksum)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, id := range changed {
			changedDocs.Add(id)
			deletedDocs.Remove(id)
		}
		deleted, err := loadDocIDSidecar(root, delta.Path, delta.DeletedDocsFile, delta.DeletedDocsChecksum)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, id := range deleted {
			deletedDocs.Add(id)
		}
	}
	return deltaEngines, changedDocs, deletedDocs, nil
}

func loadSegments(root, base string, descs []manifest.SegmentDescriptor, schemaHash string, opts Options) ([]*segment.SegmentReader, error) {
	segments := make([]*segment.SegmentReader, 0, len(descs))
	blockChecksumMode := segment.BlockChecksumDisabled
	if opts.VerifySegmentBlockChecksums {
		blockChecksumMode = segment.BlockChecksumOnOpen
	}
	readerOpts := segment.ReaderOptions{BlockChecksumMode: blockChecksumMode}
	for _, desc := range descs {
		path := filepath.Join(root, base, desc.File)
		reader, err := openSegmentReader(path, desc, readerOpts, opts.UseMmap)
		if err != nil {
			closeSegments(segments)
			return nil, err
		}
		if reader.Version() != segment.SegmentVersionV4 {
			_ = reader.Close()
			closeSegments(segments)
			return nil, fmt.Errorf("segment %s format mismatch: got segment-v%d, want segment-v%d", desc.File, reader.Version(), segment.SegmentVersionV4)
		}
		if schemaHash != "" && reader.SchemaHash() != schemaHash {
			_ = reader.Close()
			closeSegments(segments)
			return nil, fmt.Errorf("segment %s schema hash mismatch: got %s, want %s", desc.File, reader.SchemaHash(), schemaHash)
		}
		segments = append(segments, reader)
	}
	return segments, nil
}

// openSegmentReader opens one segment either by memory-mapping the file (serving
// default) or by reading + whole-file checksum verifying it into the heap.
func openSegmentReader(path string, desc manifest.SegmentDescriptor, readerOpts segment.ReaderOptions, useMmap bool) (*segment.SegmentReader, error) {
	if useMmap {
		return segment.OpenSegmentFile(path, readerOpts)
	}
	data, err := manifest.ReadAndVerify(path, desc.Size, desc.Checksum)
	if err != nil {
		return nil, err
	}
	return segment.NewSegmentReaderWithOptions(data, readerOpts)
}

func closeSegments(segments []*segment.SegmentReader) {
	for _, s := range segments {
		_ = s.Close()
	}
}

func loadDocIDSidecar(root, base, file, checksum string) ([]core.DocID, error) {
	if file == "" {
		return nil, nil
	}
	data, err := manifest.ReadAndVerify(filepath.Join(root, base, file), 0, checksum)
	if err != nil {
		return nil, err
	}
	return manifest.DecodeDocIDs(data)
}
