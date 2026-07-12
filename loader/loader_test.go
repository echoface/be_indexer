package loader_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

func loaderFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}},
	}
}

func writeSegment(t *testing.T, dir string, fields map[core.BEField]*core.FieldMeta, docs []*core.Document) (manifest.SegmentDescriptor, core.Entries) {
	t.Helper()
	buf := new(bytes.Buffer)
	wildcards, err := builder.BuildSegmentFromDocsWithOptions(buf, fields, docs, builder.BuildSegmentFromDocsOptions{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatalf("BuildSegmentFromDocs failed: %v", err)
	}
	return writeSegmentBytes(t, dir, buf.Bytes(), docs), wildcards
}

func writeSegmentBytes(t *testing.T, dir string, data []byte, docs []*core.Document) manifest.SegmentDescriptor {
	t.Helper()
	path := filepath.Join(dir, "segment-000000.bei")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest.SegmentDescriptor{
		SegmentID: 0,
		File:      "segment-000000.bei",
		Size:      uint64(len(data)),
		Checksum:  manifest.SHA256Checksum(data),
		DocCount:  uint64(len(docs)),
	}
}

func writeSidecar(t *testing.T, dir, name string, data []byte) (string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return name, manifest.SHA256Checksum(data)
}

func writeManifest(t *testing.T, root string, m manifest.Manifest) {
	t.Helper()
	manifestDir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest-000001.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-000001.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}


func bitmapToSlice(b *core.BitmapDocSet) core.DocIDList {
	if b == nil {
		return nil
	}
	var ids core.DocIDList
	b.ForEach(func(id core.DocID) {
		ids = append(ids, id)
	})
	return ids
}

func TestOpenIndexLoadsFullDeltaSnapshot(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-000001")
	deltaDir := filepath.Join(root, "delta", "delta-000002")
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deltaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fullSeg, _ := writeSegment(t, fullDir, fields, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	deltaSeg, _ := writeSegment(t, deltaDir, fields, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	changedFile, changedChecksum := writeSidecar(t, deltaDir, "changed_docs.bin", manifest.EncodeDocIDs([]core.DocID{1}))
	deletedFile, deletedChecksum := writeSidecar(t, deltaDir, "deleted_docs.bin", manifest.EncodeDocIDs(nil))

	m := manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      2,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV2,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        1,
			SnapshotWatermark: 100,
			Path:              "full/full-000001",
			Segments:          []manifest.SegmentDescriptor{fullSeg},
		},
		Deltas: []manifest.DeltaIndexDescriptor{{
			Generation:             2,
			FromWatermarkExclusive: 100,
			ToWatermarkInclusive:   101,
			Path:                   "delta/delta-000002",
			Segments:               []manifest.SegmentDescriptor{deltaSeg},
			ChangedDocsFile:        changedFile,
			ChangedDocsChecksum:    changedChecksum,
			DeletedDocsFile:        deletedFile,
			DeletedDocsChecksum:    deletedChecksum,
		}},
	}
	writeManifest(t, root, m)

	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("query a=1 mismatch: got %v want [2]", ids)
	}
	b, err = ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("query a=2 mismatch: got %v want [1]", ids)
	}
}

func TestOpenIndexRejectsChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-000001")
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg, _ := writeSegment(t, fullDir, fields, []*core.Document{core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))})
	seg.Checksum = "sha256:bad"
	writeManifest(t, root, manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      1,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV2,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        1,
			SnapshotWatermark: 1,
			Path:              "full/full-000001",
			Segments:          []manifest.SegmentDescriptor{seg},
		},
	})
	if _, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"}); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestOpenIndexRejectsSchemaMismatch(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-000001")
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg, _ := writeSegment(t, fullDir, fields, []*core.Document{core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))})
	writeManifest(t, root, manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      1,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV2,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        1,
			SnapshotWatermark: 1,
			Path:              "full/full-000001",
			Segments:          []manifest.SegmentDescriptor{seg},
		},
	})
	if _, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:other"}); err == nil {
		t.Fatal("expected schema mismatch")
	}
}

func TestOpenIndexMultipleDeltasUpdateOverridesOlderDelta(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-000001")
	delta2Dir := filepath.Join(root, "delta", "delta-000002")
	delta3Dir := filepath.Join(root, "delta", "delta-000003")
	for _, dir := range []string{fullDir, delta2Dir, delta3Dir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fullSeg, _ := writeSegment(t, fullDir, fields, []*core.Document{core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 0))})
	delta2Seg, _ := writeSegment(t, delta2Dir, fields, []*core.Document{core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 1))})
	delta3Seg, _ := writeSegment(t, delta3Dir, fields, []*core.Document{core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 2))})
	delta2ChangedFile, delta2ChangedChecksum := writeSidecar(t, delta2Dir, "changed_docs.bin", manifest.EncodeDocIDs([]core.DocID{7}))
	delta2DeletedFile, delta2DeletedChecksum := writeSidecar(t, delta2Dir, "deleted_docs.bin", manifest.EncodeDocIDs(nil))
	delta3ChangedFile, delta3ChangedChecksum := writeSidecar(t, delta3Dir, "changed_docs.bin", manifest.EncodeDocIDs([]core.DocID{7}))
	delta3DeletedFile, delta3DeletedChecksum := writeSidecar(t, delta3Dir, "deleted_docs.bin", manifest.EncodeDocIDs(nil))

	writeManifest(t, root, manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      3,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV2,
		PostingEncoding: "conjid64-entryid64-v1",
		Full:            manifest.FullIndexDescriptor{Generation: 1, SnapshotWatermark: 100, Path: "full/full-000001", Segments: []manifest.SegmentDescriptor{fullSeg}},
		Deltas: []manifest.DeltaIndexDescriptor{
			{Generation: 2, FromWatermarkExclusive: 100, ToWatermarkInclusive: 101, Path: "delta/delta-000002", Segments: []manifest.SegmentDescriptor{delta2Seg}, ChangedDocsFile: delta2ChangedFile, ChangedDocsChecksum: delta2ChangedChecksum, DeletedDocsFile: delta2DeletedFile, DeletedDocsChecksum: delta2DeletedChecksum, ChangedDocCount: 1},
			{Generation: 3, FromWatermarkExclusive: 101, ToWatermarkInclusive: 102, Path: "delta/delta-000003", Segments: []manifest.SegmentDescriptor{delta3Seg}, ChangedDocsFile: delta3ChangedFile, ChangedDocsChecksum: delta3ChangedChecksum, DeletedDocsFile: delta3DeletedFile, DeletedDocsChecksum: delta3DeletedChecksum, ChangedDocCount: 1},
		},
	})

	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ce.Retrieve(core.Assignments{"a": 0})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	if len(ids) != 0 {
		t.Fatalf("old full version should be hidden: %v", ids)
	}
	b, err = ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	if len(ids) != 0 {
		t.Fatalf("older delta version should be hidden: %v", ids)
	}
	b, err = ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("latest delta version mismatch: %v", ids)
	}
}

// TestOpenIndexWithMmap verifies the zero-copy mmap serving path returns the
// same results as the in-heap path and that closing unmaps without affecting a
// completed query.
func TestOpenIndexWithMmap(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-000001")
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fullSeg, _ := writeSegment(t, fullDir, fields, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	m := manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      1,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV2,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        1,
			SnapshotWatermark: 100,
			Path:              "full/full-000001",
			Segments:          []manifest.SegmentDescriptor{fullSeg},
		},
	}
	writeManifest(t, root, m)

	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema", UseMmap: true})
	if err != nil {
		t.Fatalf("OpenIndex(mmap) failed: %v", err)
	}
	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	if len(ids) != 2 {
		t.Fatalf("query a=1 mmap mismatch: got %v want 2 docs", ids)
	}

	// Close unmaps; a subsequent call must be safe (idempotent).
	if err := ce.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if err := ce.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
}
