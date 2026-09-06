package builder_test

import (
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

func dedupFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}},
	}
}

// §4.1.5: under FailFast a duplicate DocID is a doc-level error that poisons the
// builder (a duplicate would otherwise collide in ConjID space).
func TestFullIndexBuilder_DuplicateDocIDFailFast(t *testing.T) {
	root := t.TempDir()
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            dedupFields(),
		FailMode:          builder.FailFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := b.AddDocument(core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	if err := b.AddDocument(core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 2))); err == nil {
		t.Fatal("expected duplicate DocID to error under FailFast")
	}
	// Builder is poisoned now.
	if _, err := b.Build(); err == nil {
		t.Fatal("poisoned builder must reject Build")
	}
}

// §4.1.4 + §4.1.5: under FailSkip a duplicate DocID is skipped and leaves no
// partial state; the first version wins and the index serves correctly.
func TestFullIndexBuilder_DuplicateDocIDFailSkip(t *testing.T) {
	root := t.TempDir()
	var skipped []core.DocID
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            dedupFields(),
		FailMode:          builder.FailSkip,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
		OnSkip:            func(id core.DocID, err error) { skipped = append(skipped, id) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// doc 5 (a=1), duplicate 5 (a=2, skipped), doc 6 (a=1).
	if err := b.AddDocument(core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	if err := b.AddDocument(core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 2))); err != nil {
		t.Fatalf("FailSkip must swallow duplicate DocID, got %v", err)
	}
	if err := b.AddDocument(core.NewDocument(6).AddConjunction(core.NewConjunction().In("a", 1))); err != nil {
		t.Fatal(err)
	}
	if b.SkippedCount() != 1 || len(skipped) != 1 || skipped[0] != 5 {
		t.Fatalf("skip accounting mismatch: count=%d ids=%v", b.SkippedCount(), skipped)
	}

	full, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "dedup-test",
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
	ce, err := loader.OpenIndex(root, dedupFields(), loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}

	// The first version (a=1) won for doc 5; doc 6 also matches a=1.
	ids, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	got := map[core.DocID]bool{}
	ids.ForEach(func(id core.DocID) { got[id] = true })
	if !got[5] || !got[6] {
		t.Fatalf("want docs 5 and 6 for a=1, got %v", got)
	}
	// The skipped duplicate's value (a=2) must NOT have been written.
	ids2, err := ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	if ids2.Cardinality() != 0 {
		t.Fatalf("skipped duplicate (a=2) must not be indexed, got cardinality %d", ids2.Cardinality())
	}
}

// §4.1.4: pre-encode + atomic commit. A document whose SECOND predicate fails to
// encode must contribute nothing — not even the first, already-valid predicate.
// Under FailSkip the doc is skipped, and the resulting index must not match the
// value from the valid-but-uncommitted first predicate.
func TestFullIndexBuilder_PartialEncodeIsAtomic(t *testing.T) {
	root := t.TempDir()
	var skipped []core.DocID
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 1,
		Fields:            dedupFields(), // field "a" uses the number encoder
		FailMode:          builder.FailSkip,
		Options:           builder.BuildDirectoryOptions{SegmentSchemaHash: "sha256:schema"},
		OnSkip:            func(id core.DocID, err error) { skipped = append(skipped, id) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// A good doc first so the index is non-trivial.
	if err := b.AddDocument(core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 100))); err != nil {
		t.Fatal(err)
	}
	// doc 2: one conjunction with a valid include (a=100) AND a second conjunction
	// whose predicate fails to encode (a="notanumber"). The number encoder rejects
	// the non-numeric value, so the whole doc must be skipped atomically.
	bad := core.NewDocument(2).
		AddConjunction(core.NewConjunction().In("a", 100)).
		AddConjunction(core.NewConjunction().In("a", "notanumber"))
	if err := b.AddDocument(bad); err != nil {
		t.Fatalf("FailSkip must swallow encode error, got %v", err)
	}
	if b.SkippedCount() != 1 || len(skipped) != 1 || skipped[0] != 2 {
		t.Fatalf("skip accounting mismatch: count=%d ids=%v", b.SkippedCount(), skipped)
	}

	full, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "atomic-test",
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
	ce, err := loader.OpenIndex(root, dedupFields(), loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}

	// Only doc 1 may match a=100. If the first predicate of the skipped doc 2 had
	// leaked into the sink, doc 2 would (incorrectly) match here.
	ids, err := ce.Retrieve(core.Assignments{"a": 100})
	if err != nil {
		t.Fatal(err)
	}
	got := map[core.DocID]bool{}
	ids.ForEach(func(id core.DocID) { got[id] = true })
	if got[2] {
		t.Fatal("skipped doc 2 leaked a partially-encoded predicate into the index")
	}
	if !got[1] || len(got) != 1 {
		t.Fatalf("want only doc 1 for a=100, got %v", got)
	}
}
