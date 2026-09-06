package loader_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

func writeSimpleIndex(t *testing.T, root, manifestName string, docValue int, generation uint64) {
	t.Helper()
	fields := loaderFields()
	fullDir := filepath.Join(root, "full", "full-"+manifestName)
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seg := writeSegment(t, fullDir, fields, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", docValue)),
	})
	m := manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "test-index",
		Generation:      generation,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV4,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        generation,
			SnapshotWatermark: generation,
			Path:              "full/full-" + manifestName,
			Segments:          []manifest.SegmentDescriptor{seg},
		},
	}
	manifestDir := filepath.Join(root, "manifests")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, manifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHolderReloadKeepsOldSnapshotOnFailure(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	writeSimpleIndex(t, root, "manifest-1.json", 1, 1)
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-1.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := loader.NewHolder(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatalf("NewHolder failed: %v", err)
	}
	b, err := h.Retrieve(core.Assignments{"a": 1})
	ids := bitmapToSlice(b)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("initial query got ids=%v err=%v", ids, err)
	}

	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("missing.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err == nil {
		t.Fatal("expected reload failure")
	}
	b, err = h.Retrieve(core.Assignments{"a": 1})
	ids = bitmapToSlice(b)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("old snapshot should remain, got ids=%v err=%v", ids, err)
	}
}

func TestHolderReloadPublishesNewSnapshot(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	writeSimpleIndex(t, root, "manifest-1.json", 1, 1)
	writeSimpleIndex(t, root, "manifest-2.json", 2, 2)
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-1.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := loader.NewHolder(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-2.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	b, err := h.Retrieve(core.Assignments{"a": 2})
	ids := bitmapToSlice(b)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("new snapshot query got ids=%v err=%v", ids, err)
	}
}

func TestHolderConcurrentRetrieveAndReload(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	writeSimpleIndex(t, root, "manifest-1.json", 1, 1)
	writeSimpleIndex(t, root, "manifest-2.json", 2, 2)
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-1.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := loader.NewHolder(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = h.Retrieve(core.Assignments{"a": 1})
				_, _ = h.Retrieve(core.Assignments{"a": 2})
			}
		}()
	}
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-2.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := h.Reload(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}

func TestHolderConcurrentReloadPublishesConsistentGeneration(t *testing.T) {
	root := t.TempDir()
	fields := loaderFields()
	writeSimpleIndex(t, root, "manifest-1.json", 1, 1)
	writeSimpleIndex(t, root, "manifest-2.json", 2, 2)
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-1.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := loader.NewHolder(root, fields, loader.Options{SchemaHash: "sha256:schema"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CURRENT"), []byte("manifest-2.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.Reload(); err != nil {
				t.Errorf("Reload failed: %v", err)
			}
		}()
	}
	wg.Wait()
	cur := h.UnsafeCurrent()
	if cur == nil || cur.Snapshot() == nil || cur.Snapshot().Generation != 2 {
		t.Fatalf("expected generation 2, got %#v", cur)
	}
}
