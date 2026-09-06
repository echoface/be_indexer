package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// AtomicWriteFile writes data to path via a temporary file in the same
// directory and then atomically renames it into place.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDirBestEffort(dir)
}

// WriteManifestAtomically validates and writes a manifest JSON file.
func WriteManifestAtomically(path string, m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return AtomicWriteFile(path, data, 0o644)
}

// PublishCurrent atomically switches CURRENT to manifestRef.
func PublishCurrent(root, manifestRef string) error {
	if manifestRef == "" {
		return fmt.Errorf("manifest ref is required")
	}
	return AtomicWriteFile(filepath.Join(root, "CURRENT"), []byte(manifestRef+"\n"), 0o644)
}

// PublishManifest writes manifests/<manifestName> and then atomically updates CURRENT.
func PublishManifest(root, manifestName string, m Manifest) error {
	if manifestName == "" {
		return fmt.Errorf("manifest name is required")
	}
	if filepath.IsAbs(manifestName) || filepath.Dir(manifestName) != "." {
		return fmt.Errorf("manifest name must be a file name, got %q", manifestName)
	}
	manifestPath := filepath.Join(root, "manifests", manifestName)
	if err := WriteManifestAtomically(manifestPath, m); err != nil {
		return err
	}
	return PublishCurrent(root, manifestName)
}

// WriteDocIDsSidecar writes a deterministic DocID sidecar and returns file name and checksum.
func WriteDocIDsSidecar(dir, name string, ids []core.DocID) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("sidecar name is required")
	}
	if filepath.IsAbs(name) || filepath.Dir(name) != "." {
		return "", "", fmt.Errorf("sidecar name must be a file name, got %q", name)
	}
	idsCopy := append([]core.DocID(nil), ids...)
	sort.Slice(idsCopy, func(i, j int) bool { return idsCopy[i] < idsCopy[j] })
	data := EncodeDocIDs(idsCopy)
	checksum := SHA256Checksum(data)
	return name, checksum, AtomicWriteFile(filepath.Join(dir, name), data, 0o644)
}

func syncDirBestEffort(dir string) error {
	// Directory fsync is not supported uniformly across all platforms/filesystems.
	// Try it for stronger publication guarantees, but do not fail the write when
	// the platform rejects directory sync.
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	// Avoid surfacing transient platform-specific directory fsync errors.
	_ = d.Sync()
	return nil
}
