package manifest_test

import (
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
