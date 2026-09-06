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

// SegmentLoadMode selects both segment storage and integrity verification.
// Combining them in one enum prevents meaningless option combinations.
type SegmentLoadMode uint8

const (
	// SegmentLoadHeapVerify reads segments into the Go heap and verifies the
	// manifest size and whole-file checksum. It is the zero-value mode.
	SegmentLoadHeapVerify SegmentLoadMode = iota
	// SegmentLoadMmapVerify verifies the manifest size and whole-file checksum,
	// then maps the file read-only.
	SegmentLoadMmapVerify
	// SegmentLoadMmapTrustPublished maps the file without hashing its payload.
	// Use it only for immutable artifacts verified by a trusted publisher.
	SegmentLoadMmapTrustPublished
)

// Options controls OpenIndex storage and validation behavior.
type Options struct {
	// SegmentLoad selects heap vs mmap loading and its verification contract.
	SegmentLoad SegmentLoadMode
}

// OpenIndex loads index_root/CURRENT and returns a CompositeEngine snapshot.
func OpenIndex(root string, fields core.Schema, opts Options) (*engine.CompositeEngine, error) {
	manifestRef, err := ReadCurrentManifestRef(root)
	if err != nil {
		return nil, err
	}
	return openIndexAt(root, manifestRef, fields, opts)
}

// openIndexAt loads the exact manifest reference already observed by the
// caller. Keeping this separate from OpenIndex lets Holder.Reload compare and
// load one CURRENT value without a second read that could race with publication.
func openIndexAt(root, manifestRef string, fields core.Schema, opts Options) (*engine.CompositeEngine, error) {
	snapshot, err := loadSnapshotAt(root, manifestRef, fields, opts)
	if err != nil {
		return nil, err
	}
	return engine.NewCompositeEngine(snapshot), nil
}

// LoadSnapshot loads a manifest generation and all referenced segments/sidecars.
func LoadSnapshot(root string, fields core.Schema, opts Options) (*engine.IndexSnapshot, error) {
	manifestRef, err := ReadCurrentManifestRef(root)
	if err != nil {
		return nil, err
	}
	return loadSnapshotAt(root, manifestRef, fields, opts)
}

func loadSnapshotAt(root, manifestRef string, fields core.Schema, opts Options) (*engine.IndexSnapshot, error) {
	if opts.SegmentLoad > SegmentLoadMmapTrustPublished {
		return nil, fmt.Errorf("unknown segment load mode %d", opts.SegmentLoad)
	}
	m, err := readManifestAt(root, manifestRef)
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	runtimeSchemaHash, err := core.ComputeSchemaHash(fields)
	if err != nil {
		return nil, err
	}
	if runtimeSchemaHash != m.SchemaHash {
		return nil, fmt.Errorf("schema hash mismatch: manifest=%s runtime=%s", m.SchemaHash, runtimeSchemaHash)
	}

	fullEngine, err := loadFullEngine(root, fields, m.SchemaHash, m.Full, opts)
	if err != nil {
		return nil, fmt.Errorf("load full: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = fullEngine.Close()
		}
	}()

	deltaEngines, changedDocs, deletedDocs, err := loadDeltas(root, fields, m.SchemaHash, m.Deltas, opts)
	if err != nil {
		return nil, fmt.Errorf("load deltas: %w", err)
	}

	snapshot := &engine.IndexSnapshot{
		Generation:   m.Generation,
		FullEngine:   fullEngine,
		DeltaEngines: deltaEngines,
		ChangedDocs:  changedDocs,
		DeletedDocs:  deletedDocs,
	}
	committed = true
	return snapshot, nil
}

// ReadCurrentManifestRef reads and validates the non-empty reference stored in
// CURRENT. The returned string is the exact trimmed reference used for reload
// identity comparisons.
func ReadCurrentManifestRef(root string) (string, error) {
	currentBytes, err := os.ReadFile(filepath.Join(root, "CURRENT"))
	if err != nil {
		return "", err
	}
	current := strings.TrimSpace(string(currentBytes))
	if current == "" {
		return "", fmt.Errorf("CURRENT is empty")
	}
	return current, nil
}

// ReadCurrentManifest reads CURRENT once and then loads that exact manifest.
func ReadCurrentManifest(root string) (manifest.Manifest, error) {
	manifestRef, err := ReadCurrentManifestRef(root)
	if err != nil {
		return manifest.Manifest{}, err
	}
	return readManifestAt(root, manifestRef)
}

func readManifestAt(root, manifestRef string) (manifest.Manifest, error) {
	manifestPath := manifestRef
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

func loadFullEngine(root string, fields core.Schema, schemaHash string, full manifest.FullIndexDescriptor, opts Options) (*engine.BooleanEngine, error) {
	segments, err := loadSegments(root, full.Path, full.Segments, schemaHash, opts)
	if err != nil {
		return nil, err
	}
	fullEngine, err := engine.NewBooleanEngine(fields, segments)
	if err != nil {
		closeSegments(segments)
		return nil, err
	}
	return fullEngine, nil
}

func loadDeltas(root string, fields core.Schema, schemaHash string, deltas []manifest.DeltaIndexDescriptor, opts Options) ([]*engine.BooleanEngine, *core.BitmapDocSet, *core.BitmapDocSet, error) {
	changedDocs := core.NewBitmapDocSet()
	deletedDocs := core.NewBitmapDocSet()
	deltaEngines := make([]*engine.BooleanEngine, 0, len(deltas))
	changedByDelta := make([][]core.DocID, len(deltas))
	committed := false
	defer func() {
		if !committed {
			closeEngines(deltaEngines)
		}
	}()
	laterChangedDocs := core.NewBitmapDocSet()
	for i := len(deltas) - 1; i >= 0; i-- {
		delta := deltas[i]
		changed, err := loadDocIDSidecar(root, delta.Path, delta.ChangedDocsFile, delta.ChangedDocsChecksum)
		if err != nil {
			return nil, nil, nil, err
		}
		changedByDelta[i] = changed
		segments, err := loadSegments(root, delta.Path, delta.Segments, schemaHash, opts)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(segments) > 0 {
			deltaEngine, err := engine.NewBooleanEngine(fields, segments)
			if err != nil {
				closeSegments(segments)
				return nil, nil, nil, err
			}
			if laterChangedDocs.Cardinality() > 0 {
				ld := core.NewLiveDocs()
				laterChangedDocs.ForEach(func(id core.DocID) { ld.MarkDeleted(id) })
				deltaEngine.SetLiveDocs(ld)
			}
			deltaEngines = append(deltaEngines, deltaEngine)
		}
		for _, id := range changed {
			laterChangedDocs.Add(id)
		}
	}
	// The loop above loads newest to oldest so laterChangedDocs can mask stale
	// versions. Restore chronological engine order without repeated slice prepends.
	for left, right := 0, len(deltaEngines)-1; left < right; left, right = left+1, right-1 {
		deltaEngines[left], deltaEngines[right] = deltaEngines[right], deltaEngines[left]
	}

	for i, delta := range deltas {
		changed := changedByDelta[i]
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
	committed = true
	return deltaEngines, changedDocs, deletedDocs, nil
}

func loadSegments(root, base string, descs []manifest.SegmentDescriptor, schemaHash string, opts Options) ([]*segment.SegmentReader, error) {
	segments := make([]*segment.SegmentReader, 0, len(descs))
	for _, desc := range descs {
		path := filepath.Join(root, base, desc.File)
		reader, err := openSegmentReader(path, desc, opts)
		if err != nil {
			closeSegments(segments)
			return nil, err
		}
		if reader.Version() != segment.SegmentVersionV5 {
			_ = reader.Close()
			closeSegments(segments)
			return nil, fmt.Errorf("segment %s format mismatch: got segment-v%d, want segment-v%d", desc.File, reader.Version(), segment.SegmentVersionV5)
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
func openSegmentReader(path string, desc manifest.SegmentDescriptor, opts Options) (*segment.SegmentReader, error) {
	readerOpts := segment.ReaderOptions{BlockChecksumMode: segment.BlockChecksumDisabled}
	if opts.SegmentLoad != SegmentLoadHeapVerify {
		return segment.OpenSegmentFile(path, segment.OpenFileOptions{
			ReaderOptions:    readerOpts,
			ExpectedSize:     desc.Size,
			ExpectedChecksum: desc.Checksum,
			VerifyFile:       opts.SegmentLoad == SegmentLoadMmapVerify,
		})
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

func closeEngines(engines []*engine.BooleanEngine) {
	for _, e := range engines {
		_ = e.Close()
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
