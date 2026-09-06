package manifest

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// FormatVersionSegmentV4 identifies snapshots whose segments use the v4
	// physical format (Z-list embedded in-segment, schema hash, and per-block
	// checksums). This keeps the manifest format_version aligned with the
	// SegmentVersionV4 used by the segment footer instead of the historical
	// "segment-v2" label.
	FormatVersionSegmentV4 = "segment-v4"
)

// SegmentDescriptor describes an immutable segment file in an index snapshot.
type SegmentDescriptor struct {
	SegmentID uint32 `json:"segment_id"`
	File      string `json:"file"`
	Size      uint64 `json:"size"`
	Checksum  string `json:"checksum"`
	DocCount  uint64 `json:"doc_count"`
	MinDocID  int64  `json:"min_doc_id,omitempty"`
	MaxDocID  int64  `json:"max_doc_id,omitempty"`
}

// FullIndexDescriptor describes the long-window full index.
type FullIndexDescriptor struct {
	Generation        uint64              `json:"generation"`
	SnapshotWatermark uint64              `json:"snapshot_watermark"`
	Path              string              `json:"path"`
	Segments          []SegmentDescriptor `json:"segments"`
}

// DeltaIndexDescriptor describes a short-window delta index.
type DeltaIndexDescriptor struct {
	Generation             uint64              `json:"generation"`
	FromWatermarkExclusive uint64              `json:"from_watermark_exclusive"`
	ToWatermarkInclusive   uint64              `json:"to_watermark_inclusive"`
	Path                   string              `json:"path"`
	Segments               []SegmentDescriptor `json:"segments"`
	ChangedDocsFile        string              `json:"changed_docs_file,omitempty"`
	ChangedDocsChecksum    string              `json:"changed_docs_checksum,omitempty"`
	DeletedDocsFile        string              `json:"deleted_docs_file,omitempty"`
	DeletedDocsChecksum    string              `json:"deleted_docs_checksum,omitempty"`
	ChangedDocCount        uint64              `json:"changed_doc_count,omitempty"`
	DeletedDocCount        uint64              `json:"deleted_doc_count,omitempty"`
}

// Manifest describes a complete serving snapshot. Loading code must treat all
// referenced files as one generation and reject mixed-generation views.
type Manifest struct {
	ManifestVersion uint32 `json:"manifest_version"`
	IndexName       string `json:"index_name"`
	Generation      uint64 `json:"generation"`
	SchemaHash      string `json:"schema_hash"`
	FormatVersion   string `json:"format_version"`
	PostingEncoding string `json:"posting_encoding"`
	BuilderVersion  string `json:"builder_version,omitempty"`
	CreatedAtUnix   int64  `json:"created_at_unix,omitempty"`

	Full   FullIndexDescriptor    `json:"full"`
	Deltas []DeltaIndexDescriptor `json:"deltas,omitempty"`
}

// Validate performs structural checks that are independent of local filesystem state.
func (m Manifest) Validate() error {
	if m.ManifestVersion == 0 {
		return fmt.Errorf("manifest_version is required")
	}
	if m.IndexName == "" {
		return fmt.Errorf("index_name is required")
	}
	if m.Generation == 0 {
		return fmt.Errorf("generation is required")
	}
	if m.SchemaHash == "" {
		return fmt.Errorf("schema_hash is required")
	}
	if m.FormatVersion == "" {
		return fmt.Errorf("format_version is required")
	}
	if !IsSupportedFormatVersion(m.FormatVersion) {
		return fmt.Errorf("unsupported format_version %q", m.FormatVersion)
	}
	if m.PostingEncoding == "" {
		return fmt.Errorf("posting_encoding is required")
	}
	if err := validateFull(m.Full); err != nil {
		return err
	}
	if err := validateSafeRelPath("full path", m.Full.Path); err != nil {
		return err
	}
	if m.Full.Generation > m.Generation {
		return fmt.Errorf("full generation %d exceeds manifest generation %d", m.Full.Generation, m.Generation)
	}
	prevWatermark := m.Full.SnapshotWatermark
	seenDeltaGen := map[uint64]struct{}{}
	for i, d := range m.Deltas {
		if d.Generation == 0 {
			return fmt.Errorf("delta[%d] generation is required", i)
		}
		if _, ok := seenDeltaGen[d.Generation]; ok {
			return fmt.Errorf("duplicate delta generation %d", d.Generation)
		}
		if d.Generation > m.Generation {
			return fmt.Errorf("delta[%d] generation %d exceeds manifest generation %d", i, d.Generation, m.Generation)
		}
		seenDeltaGen[d.Generation] = struct{}{}
		if d.FromWatermarkExclusive != prevWatermark {
			return fmt.Errorf("delta[%d] watermark gap: got from=%d, want=%d", i, d.FromWatermarkExclusive, prevWatermark)
		}
		if d.ToWatermarkInclusive <= d.FromWatermarkExclusive {
			return fmt.Errorf("delta[%d] invalid watermark range", i)
		}
		if d.Path == "" {
			return fmt.Errorf("delta[%d] path is required", i)
		}
		if err := validateSafeRelPath(fmt.Sprintf("delta[%d] path", i), d.Path); err != nil {
			return err
		}
		if err := validateDeltaSegments(fmt.Sprintf("delta[%d]", i), d); err != nil {
			return err
		}
		if err := validateOptionalSidecar(fmt.Sprintf("delta[%d] changed_docs", i), d.ChangedDocsFile, d.ChangedDocsChecksum); err != nil {
			return err
		}
		if err := validateOptionalSidecar(fmt.Sprintf("delta[%d] deleted_docs", i), d.DeletedDocsFile, d.DeletedDocsChecksum); err != nil {
			return err
		}
		prevWatermark = d.ToWatermarkInclusive
	}
	return nil
}

// IsSupportedFormatVersion reports whether a manifest format version is loadable.
func IsSupportedFormatVersion(format string) bool {
	return format == FormatVersionSegmentV4
}

func validateFull(f FullIndexDescriptor) error {
	if f.Generation == 0 {
		return fmt.Errorf("full generation is required")
	}
	if f.Path == "" {
		return fmt.Errorf("full path is required")
	}
	return validateSegments("full", f.Segments)
}

func validateSegments(owner string, segments []SegmentDescriptor) error {
	if len(segments) == 0 {
		return fmt.Errorf("%s segments are required", owner)
	}
	seen := map[uint32]struct{}{}
	for i, s := range segments {
		if _, ok := seen[s.SegmentID]; ok {
			return fmt.Errorf("%s segment[%d] duplicate segment_id %d", owner, i, s.SegmentID)
		}
		seen[s.SegmentID] = struct{}{}
		if s.File == "" {
			return fmt.Errorf("%s segment[%d] file is required", owner, i)
		}
		if err := validateSafeFileName(fmt.Sprintf("%s segment[%d] file", owner, i), s.File); err != nil {
			return err
		}
		if s.Size == 0 {
			return fmt.Errorf("%s segment[%d] size is required", owner, i)
		}
		if s.Checksum == "" {
			return fmt.Errorf("%s segment[%d] checksum is required", owner, i)
		}
	}
	return nil
}

func validateDeltaSegments(owner string, d DeltaIndexDescriptor) error {
	if len(d.Segments) == 0 {
		if d.ChangedDocsFile == "" || d.ChangedDocsChecksum == "" {
			return fmt.Errorf("%s delete-only delta requires changed_docs sidecar", owner)
		}
		return nil
	}
	return validateSegments(owner, d.Segments)
}

func validateOptionalSidecar(owner, file, checksum string) error {
	if (file == "") != (checksum == "") {
		return fmt.Errorf("%s file and checksum must be specified together", owner)
	}
	if file != "" {
		if err := validateSafeFileName(owner+" file", file); err != nil {
			return err
		}
	}
	return nil
}

func validateSafeRelPath(owner, p string) error {
	if p == "" {
		return fmt.Errorf("%s is required", owner)
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s must be relative, got %q", owner, p)
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s must not escape index root, got %q", owner, p)
	}
	return nil
}

func validateSafeFileName(owner, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", owner)
	}
	if filepath.IsAbs(name) || filepath.Dir(name) != "." || strings.Contains(name, "..") {
		return fmt.Errorf("%s must be a safe file name, got %q", owner, name)
	}
	return nil
}
