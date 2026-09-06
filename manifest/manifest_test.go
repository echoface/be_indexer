package manifest_test

import (
	"testing"

	"github.com/echoface/be_indexer/manifest"
)

func validManifest() manifest.Manifest {
	return manifest.Manifest{
		ManifestVersion: 1,
		IndexName:       "ad-targeting",
		Generation:      3,
		SchemaHash:      "sha256:schema",
		FormatVersion:   manifest.FormatVersionSegmentV5,
		PostingEncoding: "conjid64-entryid64-v1",
		Full: manifest.FullIndexDescriptor{
			Generation:        1,
			SnapshotWatermark: 100,
			Path:              "full/full-1",
			Segments: []manifest.SegmentDescriptor{{
				SegmentID: 0,
				File:      "segment-0.bei",
				Size:      1024,
				Checksum:  "sha256:seg0",
				DocCount:  10,
			}},
		},
		Deltas: []manifest.DeltaIndexDescriptor{{
			Generation:             2,
			FromWatermarkExclusive: 100,
			ToWatermarkInclusive:   120,
			Path:                   "delta/delta-2",
			Segments: []manifest.SegmentDescriptor{{
				SegmentID: 0,
				File:      "segment-0.bei",
				Size:      128,
				Checksum:  "sha256:delta0",
				DocCount:  1,
			}},
		}},
	}
}

func TestManifestValidateOK(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestManifestValidateRequiresSchemaHash(t *testing.T) {
	m := validManifest()
	m.SchemaHash = ""
	if err := m.Validate(); err == nil {
		t.Fatal("expected schema hash validation error")
	}
}

func TestManifestValidateRejectsUnsupportedFormatVersion(t *testing.T) {
	m := validManifest()
	m.FormatVersion = "segment-v9"
	if err := m.Validate(); err == nil {
		t.Fatal("expected unsupported format version validation error")
	}
}

func TestManifestValidateRequiresIndexName(t *testing.T) {
	m := validManifest()
	m.IndexName = ""
	if err := m.Validate(); err == nil {
		t.Fatal("expected index name validation error")
	}
}

func TestManifestValidateRejectsWatermarkGap(t *testing.T) {
	m := validManifest()
	m.Deltas[0].FromWatermarkExclusive = 99
	if err := m.Validate(); err == nil {
		t.Fatal("expected watermark gap validation error")
	}
}

func TestManifestValidateRejectsDuplicateSegmentID(t *testing.T) {
	m := validManifest()
	m.Full.Segments = append(m.Full.Segments, manifest.SegmentDescriptor{
		SegmentID: 0,
		File:      "segment-dup.bei",
		Size:      1,
		Checksum:  "sha256:dup",
	})
	if err := m.Validate(); err == nil {
		t.Fatal("expected duplicate segment id validation error")
	}
}

func TestManifestValidateRejectsDeltaGenerationAfterManifest(t *testing.T) {
	m := validManifest()
	m.Deltas[0].Generation = m.Generation + 1
	if err := m.Validate(); err == nil {
		t.Fatal("expected delta generation validation error")
	}
}

func TestManifestValidateRejectsUnpairedSidecarChecksum(t *testing.T) {
	m := validManifest()
	m.Deltas[0].ChangedDocsFile = "changed_docs.bin"
	if err := m.Validate(); err == nil {
		t.Fatal("expected sidecar checksum validation error")
	}
}

func TestManifestValidateAllowsDeleteOnlyDelta(t *testing.T) {
	m := validManifest()
	m.Deltas[0].Segments = nil
	m.Deltas[0].ChangedDocsFile = "changed_docs.bin"
	m.Deltas[0].ChangedDocsChecksum = "sha256:changed"
	m.Deltas[0].DeletedDocsFile = "deleted_docs.bin"
	m.Deltas[0].DeletedDocsChecksum = "sha256:deleted"
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate delete-only delta failed: %v", err)
	}
}

func TestManifestValidateRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*manifest.Manifest)
	}{
		{
			name: "absolute full path",
			mut:  func(m *manifest.Manifest) { m.Full.Path = "/tmp/full" },
		},
		{
			name: "escaping delta path",
			mut:  func(m *manifest.Manifest) { m.Deltas[0].Path = "../delta" },
		},
		{
			name: "segment file with directory",
			mut:  func(m *manifest.Manifest) { m.Full.Segments[0].File = "nested/segment.bei" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest()
			tt.mut(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("expected unsafe path validation error")
			}
		})
	}
}
