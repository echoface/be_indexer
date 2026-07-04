package builder_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

func artifactFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}},
	}
}

func assertArtifactIDs(t *testing.T, got core.DocIDList, want ...core.DocID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids length mismatch: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids mismatch: got=%v want=%v", got, want)
		}
	}
}

func TestBuildFullIndexDirWritesSegmentsAndWildcards(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Documents: []*core.Document{
			core.NewDocument(10).AddConjunction(core.NewConjunction().In("a", 1)),
			core.NewDocument(20).AddConjunction(core.NewConjunction().In("a", 2)),
		},
		Options: builder.BuildDirectoryOptions{MaxDocsPerSegment: 1},
	})
	if err != nil {
		t.Fatalf("BuildFullIndexDir failed: %v", err)
	}
	if full.Path != "full/full-000001" {
		t.Fatalf("full path mismatch: %s", full.Path)
	}
	if len(full.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(full.Segments))
	}
	for _, seg := range full.Segments {
		data, err := os.ReadFile(filepath.Join(root, full.Path, seg.File))
		if err != nil {
			t.Fatal(err)
		}
		if seg.Size != uint64(len(data)) || seg.Checksum != manifest.SHA256Checksum(data) {
			t.Fatalf("segment descriptor mismatch: %#v", seg)
		}
		if seg.DocCount != 1 || seg.MinDocID == 0 || seg.MaxDocID == 0 {
			t.Fatalf("segment doc stats mismatch: %#v", seg)
		}
	}
}

func TestBuildArtifactThenOpenIndexEndToEnd(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Documents: []*core.Document{
			core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
			core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
			core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 3)),
		},
		Options: builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatalf("BuildFullIndexDir failed: %v", err)
	}
	delta, err := builder.BuildDeltaIndexDir(builder.DeltaBuildRequest{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 100,
		ToWatermarkInclusive:   110,
		Fields:                 fields,
		Mutations: []builder.Mutation{
			{DocID: 1, Version: 1, Op: builder.MutationUpsert, Document: core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2))},
			{DocID: 2, Version: 1, Op: builder.MutationDelete},
			{DocID: 3, Version: 1, Op: builder.MutationDelete},
			{DocID: 3, Version: 2, Op: builder.MutationUpsert, Document: core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 4))},
		},
		Options: builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatalf("BuildDeltaIndexDir failed: %v", err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "ad-targeting-test",
		Generation: 2,
		SchemaHash: "sha256:schema",
		Full:       full,
		Deltas:     []manifest.DeltaIndexDescriptor{delta},
	})
	if err != nil {
		t.Fatalf("NewSnapshotManifest failed: %v", err)
	}
	if err := manifest.PublishManifest(root, "manifest-000002.json", m); err != nil {
		t.Fatalf("PublishManifest failed: %v", err)
	}
	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatalf("OpenIndex failed: %v", err)
	}
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids)
	ids, err = ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 1)
	ids, err = ce.Retrieve(core.Assignments{"a": 4})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 3)
}

func TestBuildFullIndexDirFromIteratorEndToEnd(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 2)),
	}
	idx := 0
	full, err := builder.BuildFullIndexDirFromIterator(builder.FullStreamBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Documents: builder.DocumentIteratorFunc(func() (*core.Document, bool, error) {
			if idx >= len(docs) {
				return nil, false, nil
			}
			doc := docs[idx]
			idx++
			return doc, true, nil
		}),
		Options: builder.BuildDirectoryOptions{MaxDocsPerSegment: 2, MaxPostingsInMemory: 1, SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatalf("BuildFullIndexDirFromIterator failed: %v", err)
	}
	if len(full.Segments) != 2 {
		t.Fatalf("expected 2 streaming segments, got %d", len(full.Segments))
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "stream-full-test",
		Generation: 1,
		SchemaHash: "sha256:schema",
		Full:       full,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.PublishManifest(root, "manifest-000001.json", m); err != nil {
		t.Fatal(err)
	}
	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 1, 2)
	ids, err = ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 3)
	if matches, err := filepath.Glob(filepath.Join(root, "full", "full-000001", ".runs", "*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("expected run files to be cleaned, got %v", matches)
	}
	if matches, err := filepath.Glob(filepath.Join(root, "full", "full-000001", ".wildcard-runs", "*")); err != nil {
		t.Fatal(err)
	} else if len(matches) != 0 {
		t.Fatalf("expected wildcard run files to be cleaned, got %v", matches)
	}
}

func TestBuildFullIndexDirSegmentV2EmbedsWildcards(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Documents: []*core.Document{
			core.NewDocument(100).AddConjunction(core.NewConjunction().NotIn("a", 99)),
			core.NewDocument(1).AddConjunction(core.NewConjunction().NotIn("a", 99)),
		},
		Options: builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema", MaxDocsPerSegment: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "segment-v2-test",
		Generation: 1,
		SchemaHash: "sha256:schema",
		Full:       full,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.PublishManifest(root, "manifest-000001.json", m); err != nil {
		t.Fatal(err)
	}
	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ce.Retrieve(core.Assignments{})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 1, 100)
	ids, err = ce.Retrieve(core.Assignments{"a": 99})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids)
}

func TestBuildDeltaIndexDirDeleteOnly(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 10,
		Fields:            fields,
		Documents:         []*core.Document{core.NewDocument(9).AddConjunction(core.NewConjunction().In("a", 1))},
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, err := builder.BuildDeltaIndexDir(builder.DeltaBuildRequest{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 fields,
		Mutations:              []builder.Mutation{{DocID: 9, Version: 1, Op: builder.MutationDelete}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Segments) != 0 {
		t.Fatalf("delete-only delta should not write segments: %#v", delta.Segments)
	}
	if delta.ChangedDocCount != 1 || delta.DeletedDocCount != 1 {
		t.Fatalf("delta change counts mismatch: changed=%d deleted=%d", delta.ChangedDocCount, delta.DeletedDocCount)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "delete-only-test",
		Generation: 2,
		SchemaHash: "sha256:schema",
		Full:       full,
		Deltas:     []manifest.DeltaIndexDescriptor{delta},
	})
	if err != nil {
		t.Fatalf("NewSnapshotManifest failed: %v", err)
	}
	if err := manifest.PublishManifest(root, "manifest-000002.json", m); err != nil {
		t.Fatal(err)
	}
	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids)
}

func TestBuildArtifactRejectsExistingFinalDirWithoutDeleting(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	finalDir := filepath.Join(root, "full", "full-000001")
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(finalDir, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            fields,
		Documents:         []*core.Document{core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))},
	})
	if err == nil {
		t.Fatal("expected existing target directory error")
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "keep" {
		t.Fatalf("existing directory was modified: %q", data)
	}
}

func TestBuildFullIndexDirRejectsEmptyDocuments(t *testing.T) {
	_, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              t.TempDir(),
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            artifactFields(),
	})
	if err == nil {
		t.Fatal("expected empty full documents error")
	}
}

func TestOpenIndexMultipleDeltasDeleteThenRecreate(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full, err := builder.BuildFullIndexDir(builder.FullBuildRequest{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 10,
		Fields:            fields,
		Documents:         []*core.Document{core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 1))},
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deleteDelta, err := builder.BuildDeltaIndexDir(builder.DeltaBuildRequest{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 fields,
		Mutations:              []builder.Mutation{{DocID: 7, Version: 1, Op: builder.MutationDelete}},
		Options:                builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatal(err)
	}
	recreateDelta, err := builder.BuildDeltaIndexDir(builder.DeltaBuildRequest{
		Root:                   root,
		Generation:             3,
		FromWatermarkExclusive: 11,
		ToWatermarkInclusive:   12,
		Fields:                 fields,
		Mutations: []builder.Mutation{
			{DocID: 7, Version: 2, Op: builder.MutationUpsert, Document: core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 2))},
		},
		Options: builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "multi-delta-test",
		Generation: 3,
		SchemaHash: "sha256:schema",
		Full:       full,
		Deltas:     []manifest.DeltaIndexDescriptor{deleteDelta, recreateDelta},
	})
	if err != nil {
		t.Fatalf("NewSnapshotManifest failed: %v", err)
	}
	if err := manifest.PublishManifest(root, "manifest-000003.json", m); err != nil {
		t.Fatal(err)
	}
	ce, err := loader.OpenIndex(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids)
	ids, err = ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 7)
}
