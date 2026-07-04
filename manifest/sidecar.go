package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/echoface/be_indexer/core"
)

const (
	entriesSidecarMagic = "BEIENT1\x00"
	docIDsSidecarMagic  = "BEIDOC1\x00"
	sidecarHeaderSize   = 16 // magic(8) + count(uint64)
)

// SHA256Checksum returns the manifest checksum representation for data.
func SHA256Checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SHA256File returns file size and sha256 checksum without loading the whole file into memory.
func SHA256File(path string) (uint64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return uint64(n), "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyBytes checks size and sha256 checksum when the expected values are provided.
func VerifyBytes(data []byte, expectedSize uint64, expectedChecksum string) error {
	if expectedSize != 0 && uint64(len(data)) != expectedSize {
		return fmt.Errorf("size mismatch: got %d, want %d", len(data), expectedSize)
	}
	if expectedChecksum != "" && SHA256Checksum(data) != expectedChecksum {
		return fmt.Errorf("checksum mismatch")
	}
	return nil
}

// ReadAndVerify reads a file and verifies size/checksum.
func ReadAndVerify(path string, expectedSize uint64, expectedChecksum string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := VerifyBytes(data, expectedSize, expectedChecksum); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return data, nil
}

// EncodeEntries serializes wildcard/Z-list entries into a deterministic sidecar.
func EncodeEntries(entries core.Entries) []byte {
	buf := make([]byte, sidecarHeaderSize+len(entries)*8)
	copy(buf[:8], entriesSidecarMagic)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(len(entries)))
	for i, entry := range entries {
		binary.LittleEndian.PutUint64(buf[sidecarHeaderSize+i*8:sidecarHeaderSize+(i+1)*8], uint64(entry))
	}
	return buf
}

// DecodeEntries deserializes wildcard/Z-list entries.
func DecodeEntries(data []byte) (core.Entries, error) {
	if len(data) < sidecarHeaderSize || string(data[:8]) != entriesSidecarMagic {
		return nil, fmt.Errorf("invalid entries sidecar")
	}
	count := binary.LittleEndian.Uint64(data[8:16])
	if count > uint64((len(data)-sidecarHeaderSize)/8) || len(data) != sidecarHeaderSize+int(count)*8 {
		return nil, fmt.Errorf("truncated entries sidecar")
	}
	entries := make(core.Entries, count)
	for i := range entries {
		entries[i] = core.EntryID(binary.LittleEndian.Uint64(data[sidecarHeaderSize+i*8 : sidecarHeaderSize+(i+1)*8]))
	}
	return entries, nil
}

// EncodeDocIDs serializes a DocID set sidecar such as changed_docs or deleted_docs.
func EncodeDocIDs(ids []core.DocID) []byte {
	buf := make([]byte, sidecarHeaderSize+len(ids)*8)
	copy(buf[:8], docIDsSidecarMagic)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(len(ids)))
	for i, id := range ids {
		binary.LittleEndian.PutUint64(buf[sidecarHeaderSize+i*8:sidecarHeaderSize+(i+1)*8], uint64(id))
	}
	return buf
}

// DecodeDocIDs deserializes a DocID sidecar.
func DecodeDocIDs(data []byte) ([]core.DocID, error) {
	if len(data) < sidecarHeaderSize || string(data[:8]) != docIDsSidecarMagic {
		return nil, fmt.Errorf("invalid doc ids sidecar")
	}
	count := binary.LittleEndian.Uint64(data[8:16])
	if count > uint64((len(data)-sidecarHeaderSize)/8) || len(data) != sidecarHeaderSize+int(count)*8 {
		return nil, fmt.Errorf("truncated doc ids sidecar")
	}
	ids := make([]core.DocID, count)
	for i := range ids {
		ids[i] = core.DocID(binary.LittleEndian.Uint64(data[sidecarHeaderSize+i*8 : sidecarHeaderSize+(i+1)*8]))
	}
	return ids, nil
}

// IsSHA256Checksum reports whether checksum uses the supported sha256 format.
func IsSHA256Checksum(checksum string) bool {
	return strings.HasPrefix(checksum, "sha256:") && len(checksum) == len("sha256:")+sha256.Size*2
}
