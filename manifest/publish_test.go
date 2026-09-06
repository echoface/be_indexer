package manifest_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/manifest"
)

func TestPublishManifestWritesCurrentAtomically(t *testing.T) {
	root := t.TempDir()
	m := validManifest()
	if err := manifest.PublishManifest(root, "manifest-000001.json", m); err != nil {
		t.Fatalf("PublishManifest failed: %v", err)
	}
	current, err := os.ReadFile(filepath.Join(root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != "manifest-000001.json\n" {
		t.Fatalf("unexpected CURRENT: %q", current)
	}
	if _, err := os.Stat(filepath.Join(root, "manifests", "manifest-000001.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishManifestRejectsInvalidName(t *testing.T) {
	if err := manifest.PublishManifest(t.TempDir(), "nested/manifest.json", validManifest()); err == nil {
		t.Fatal("expected invalid manifest name error")
	}
}

func TestPublishManifestRejectsExistingImmutableReference(t *testing.T) {
	root := t.TempDir()
	name := "manifest-000001.json"
	original := validManifest()
	if err := manifest.PublishManifest(root, name, original); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifests", name)
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	replacement := validManifest()
	replacement.Generation++
	if err := manifest.PublishManifest(root, name, replacement); !errors.Is(err, manifest.ErrManifestExists) {
		t.Fatalf("expected ErrManifestExists, got %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("existing manifest content was modified")
	}
	current, err := os.ReadFile(filepath.Join(root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != name+"\n" {
		t.Fatalf("CURRENT changed after rejected publish: %q", current)
	}
}

func TestWriteSidecarsAreSortedAndLoadable(t *testing.T) {
	dir := t.TempDir()
	file, checksum, err := manifest.WriteDocIDsSidecar(dir, "changed_docs.bin", []core.DocID{9, 1, 5})
	if err != nil {
		t.Fatal(err)
	}
	data, err := manifest.ReadAndVerify(filepath.Join(dir, file), 0, checksum)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := manifest.DecodeDocIDs(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DocID{1, 5, 9}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids not sorted: got=%v want=%v", ids, want)
		}
	}
}
