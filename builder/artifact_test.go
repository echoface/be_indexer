package builder_test

import (
	"errors"
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
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}},
	}
}

func assertArtifactIDs(t *testing.T, got *core.BitmapDocSet, want ...core.DocID) {
	t.Helper()
	gotSlice := bitmapToSlice(got)
	if len(gotSlice) != len(want) {
		t.Fatalf("ids length mismatch: got=%v want=%v", gotSlice, want)
	}
	for i := range want {
		if gotSlice[i] != want[i] {
			t.Fatalf("ids mismatch: got=%v want=%v", gotSlice, want)
		}
	}
}

func bitmapToSlice(b *core.BitmapDocSet) core.DocIDList {
	var ids core.DocIDList
	b.ForEach(func(id core.DocID) { ids = append(ids, id) })
	return ids
}

// buildFull is a small helper that pushes docs into a FullIndexBuilder and
// commits, mirroring the common call pattern.
func buildFull(t *testing.T, opt builder.FullIndexBuildOption, docs []*core.Document) manifest.FullIndexDescriptor {
	t.Helper()
	b, err := builder.NewFullIndexBuilder(opt)
	if err != nil {
		t.Fatalf("NewFullIndexBuilder failed: %v", err)
	}
	defer b.Close()
	for _, doc := range docs {
		if err := b.AddDocument(doc); err != nil {
			t.Fatalf("AddDocument failed: %v", err)
		}
	}
	full, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return full
}

func buildDelta(t *testing.T, opt builder.DeltaIndexBuildOption, mutations []builder.Mutation) manifest.DeltaIndexDescriptor {
	t.Helper()
	b, err := builder.NewDeltaIndexBuilder(opt)
	if err != nil {
		t.Fatalf("NewDeltaIndexBuilder failed: %v", err)
	}
	defer b.Close()
	for _, m := range mutations {
		if err := b.AddMutation(m); err != nil {
			t.Fatalf("AddMutation failed: %v", err)
		}
	}
	delta, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	return delta
}

func TestFullIndexBuilderWritesSegmentsAndDescriptors(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{MaxDocsPerSegment: 1},
	}, []*core.Document{
		core.NewDocument(10).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(20).AddConjunction(core.NewConjunction().In("a", 2)),
	})
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
		// Descriptor is self-describing & verifiable (size + sha256).
		if seg.Size != uint64(len(data)) || seg.Checksum != manifest.SHA256Checksum(data) {
			t.Fatalf("segment descriptor mismatch: %#v", seg)
		}
		if seg.DocCount != 1 || seg.MinDocID == 0 || seg.MaxDocID == 0 {
			t.Fatalf("segment doc stats mismatch: %#v", seg)
		}
	}
	// tmp dir must be consumed by rename (no residue under root/tmp).
	if matches, _ := filepath.Glob(filepath.Join(root, "tmp", "*")); len(matches) != 0 {
		t.Fatalf("expected tmp cleaned, got %v", matches)
	}
	// run/wildcard-run scratch dirs must be cleaned inside the published dir.
	if matches, _ := filepath.Glob(filepath.Join(root, full.Path, ".runs", "*")); len(matches) != 0 {
		t.Fatalf("expected run files cleaned, got %v", matches)
	}
	if matches, _ := filepath.Glob(filepath.Join(root, full.Path, ".wildcard-runs", "*")); len(matches) != 0 {
		t.Fatalf("expected wildcard run files cleaned, got %v", matches)
	}
}

func TestBuildArtifactThenOpenIndexEndToEnd(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 3)),
	})
	delta := buildDelta(t, builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 100,
		ToWatermarkInclusive:   110,
		Fields:                 fields,
		Options:                builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []builder.Mutation{
		{DocID: 1, Version: 1, Op: builder.MutationUpsert, Document: core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2))},
		{DocID: 2, Version: 1, Op: builder.MutationDelete},
		{DocID: 3, Version: 1, Op: builder.MutationDelete},
		{DocID: 3, Version: 2, Op: builder.MutationUpsert, Document: core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 4))},
	})
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

func TestFullIndexBuilderStreamingMultiSegmentEndToEnd(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{MaxDocsPerSegment: 2, MaxPostingsInMemory: 1, SegmentSchemaHash: "sha256:schema"},
	}, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 2)),
	})
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
}

func TestFullIndexBuilderSegmentV2EmbedsWildcards(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema", MaxDocsPerSegment: 1},
	}, []*core.Document{
		core.NewDocument(100).AddConjunction(core.NewConjunction().NotIn("a", 99)),
		core.NewDocument(1).AddConjunction(core.NewConjunction().NotIn("a", 99)),
	})
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

func TestDeltaIndexBuilderDeleteOnly(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 10,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []*core.Document{core.NewDocument(9).AddConjunction(core.NewConjunction().In("a", 1))})

	delta := buildDelta(t, builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 fields,
	}, []builder.Mutation{{DocID: 9, Version: 1, Op: builder.MutationDelete}})

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

func TestNewFullIndexBuilderRejectsExistingGeneration(t *testing.T) {
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
	_, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            fields,
	})
	if !errors.Is(err, builder.ErrGenerationExists) {
		t.Fatalf("expected ErrGenerationExists, got %v", err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "keep" {
		t.Fatalf("existing directory was modified: %q", data)
	}
}

func TestFullIndexBuilderRejectsEmptyDocuments(t *testing.T) {
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              t.TempDir(),
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            artifactFields(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Build(); err == nil {
		t.Fatal("expected empty full documents error")
	}
}

func TestDeltaIndexBuilderEmptyIsValid(t *testing.T) {
	root := t.TempDir()
	b, err := builder.NewDeltaIndexBuilder(builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 artifactFields(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	delta, err := b.Build()
	if err != nil {
		t.Fatalf("empty delta should be valid: %v", err)
	}
	if delta.ChangedDocCount != 0 || delta.DeletedDocCount != 0 || len(delta.Segments) != 0 {
		t.Fatalf("empty delta descriptor mismatch: %#v", delta)
	}
	if delta.ChangedDocsFile == "" || delta.DeletedDocsFile == "" {
		t.Fatalf("empty delta should still write sidecars: %#v", delta)
	}
}

func TestFullIndexBuilderFailFastPoisonsBuilder(t *testing.T) {
	root := t.TempDir()
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            artifactFields(),
		FailMode:          builder.FailFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// A conjunction referencing an unindexed field is fine (skipped); force a
	// real doc-level error via an out-of-range DocID.
	bad := core.NewDocument(1 << 60).AddConjunction(core.NewConjunction().In("a", 1))
	if err := b.AddDocument(bad); err == nil {
		t.Fatal("expected doc-level error under FailFast")
	}
	// Subsequent operations must be rejected.
	if err := b.AddDocument(core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))); err == nil {
		t.Fatal("expected poisoned builder to reject AddDocument")
	}
	if _, err := b.Build(); err == nil {
		t.Fatal("expected poisoned builder to reject Build")
	}
	// tmp must be cleaned by the internal fatal path.
	if matches, _ := filepath.Glob(filepath.Join(root, "tmp", "*")); len(matches) != 0 {
		t.Fatalf("expected tmp cleaned after fatal, got %v", matches)
	}
	// final dir must not exist.
	if _, statErr := os.Stat(filepath.Join(root, "full", "full-000001")); !os.IsNotExist(statErr) {
		t.Fatalf("final dir should not exist after failed build")
	}
}

func TestFullIndexBuilderFailSkipContinues(t *testing.T) {
	root := t.TempDir()
	var skippedIDs []core.DocID
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            artifactFields(),
		FailMode:          builder.FailSkip,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
		OnSkip: func(docID core.DocID, err error) {
			skippedIDs = append(skippedIDs, docID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// Good doc, bad doc (out-of-range id), good doc.
	if err := b.AddDocument(core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	if err := b.AddDocument(core.NewDocument(1 << 60).AddConjunction(core.NewConjunction().In("a", 2))); err != nil {
		t.Fatalf("FailSkip should swallow doc error, got %v", err)
	}
	if err := b.AddDocument(core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	if b.SkippedCount() != 1 || len(skippedIDs) != 1 {
		t.Fatalf("skip accounting mismatch: count=%d ids=%v", b.SkippedCount(), skippedIDs)
	}
	full, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "failskip-test",
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
	ce, err := loader.OpenIndex(root, artifactFields(), loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	// Only good docs (1,2) are present; the skipped doc contributed nothing.
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	assertArtifactIDs(t, ids, 1, 2)
}

func TestFullIndexBuilderCloseIdempotentAbortsTmp(t *testing.T) {
	root := t.TempDir()
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            artifactFields(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AddDocument(core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	// Abort before Build.
	if err := b.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Second Close is a no-op.
	if err := b.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
	// tmp cleaned, no final dir.
	if matches, _ := filepath.Glob(filepath.Join(root, "tmp", "*")); len(matches) != 0 {
		t.Fatalf("expected tmp cleaned after abort, got %v", matches)
	}
	if _, statErr := os.Stat(filepath.Join(root, "full", "full-000001")); !os.IsNotExist(statErr) {
		t.Fatal("final dir should not exist after abort")
	}
	// Add/Build after Close are rejected.
	if err := b.AddDocument(core.NewDocument(2)); !errors.Is(err, builder.ErrBuilderClosed) {
		t.Fatalf("expected ErrBuilderClosed, got %v", err)
	}
}

func TestDeltaIndexBuilderMaxMutations(t *testing.T) {
	root := t.TempDir()
	b, err := builder.NewDeltaIndexBuilder(builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 artifactFields(),
		MaxMutations:           1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := b.AddMutation(builder.Mutation{DocID: 1, Version: 1, Op: builder.MutationDelete}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddMutation(builder.Mutation{DocID: 2, Version: 1, Op: builder.MutationDelete}); !errors.Is(err, builder.ErrTooManyMutations) {
		t.Fatalf("expected ErrTooManyMutations, got %v", err)
	}
}

func TestOpenIndexMultipleDeltasDeleteThenRecreate(t *testing.T) {
	root := t.TempDir()
	fields := artifactFields()
	full := buildFull(t, builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 10,
		Fields:            fields,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []*core.Document{core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 1))})

	deleteDelta := buildDelta(t, builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 10,
		ToWatermarkInclusive:   11,
		Fields:                 fields,
		Options:                builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []builder.Mutation{{DocID: 7, Version: 1, Op: builder.MutationDelete}})

	recreateDelta := buildDelta(t, builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             3,
		FromWatermarkExclusive: 11,
		ToWatermarkInclusive:   12,
		Fields:                 fields,
		Options:                builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
	}, []builder.Mutation{
		{DocID: 7, Version: 2, Op: builder.MutationUpsert, Document: core.NewDocument(7).AddConjunction(core.NewConjunction().In("a", 2))},
	})

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
