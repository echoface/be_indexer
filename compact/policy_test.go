package compact_test

import (
	"testing"
	"time"

	"github.com/echoface/be_indexer/compact"
	"github.com/echoface/be_indexer/manifest"
)

func compactManifest() manifest.Manifest {
	return manifest.Manifest{
		Generation: 2,
		Full: manifest.FullIndexDescriptor{
			Generation: 1,
			Segments: []manifest.SegmentDescriptor{
				{SegmentID: 0, Size: 600, DocCount: 60},
				{SegmentID: 1, Size: 400, DocCount: 40},
			},
		},
	}
}

func addDelta(m *manifest.Manifest, gen uint64, segs int, docs, bytes, changed, deleted uint64) {
	d := manifest.DeltaIndexDescriptor{Generation: gen, ChangedDocCount: changed, DeletedDocCount: deleted}
	for i := 0; i < segs; i++ {
		d.Segments = append(d.Segments, manifest.SegmentDescriptor{SegmentID: uint32(i), Size: bytes / uint64(segs), DocCount: docs / uint64(segs)})
	}
	if changed > 0 {
		d.ChangedDocsFile = "changed_docs.bin"
	}
	if deleted > 0 {
		d.DeletedDocsFile = "deleted_docs.bin"
	}
	m.Deltas = append(m.Deltas, d)
}

func TestCollectStats(t *testing.T) {
	m := compactManifest()
	addDelta(&m, 2, 2, 10, 100, 6, 2)
	s := compact.CollectStats(m)
	if s.FullDocCount != 100 || s.FullBytes != 1000 || s.FullSegments != 2 {
		t.Fatalf("full stats mismatch: %#v", s)
	}
	if s.DeltaDocCount != 10 || s.DeltaBytes != 100 || s.DeltaSegments != 2 {
		t.Fatalf("delta stats mismatch: %#v", s)
	}
	if s.ChangedDocCount != 6 || s.DeletedDocCount != 2 || !s.HasExactChangeCounts {
		t.Fatalf("change stats mismatch: %#v", s)
	}
	if s.DeltaDocsRatio != 0.1 || s.DeltaBytesRatio != 0.1 || s.ChangedDocsRatio != 0.06 {
		t.Fatalf("ratio mismatch: %#v", s)
	}
}

func TestDecideNoneAtThreshold(t *testing.T) {
	m := compactManifest()
	addDelta(&m, 2, 16, 5, 100, 5, 1)
	rec := compact.Decide(m, compact.Options{})
	if rec.Decision != compact.DecisionNone {
		t.Fatalf("expected none at threshold, got %#v", rec)
	}
}

func TestDecideMinorBySegmentsAndP99(t *testing.T) {
	m := compactManifest()
	addDelta(&m, 2, 17, 1, 10, 1, 0)
	rec := compact.Decide(m, compact.Options{})
	if rec.Decision != compact.DecisionMinor || rec.Reasons[0].Code != "minor_delta_segments" {
		t.Fatalf("expected minor by segments, got %#v", rec)
	}

	rec = compact.Decide(compactManifest(), compact.Options{Observation: compact.RuntimeObservation{FullOnlyP99: time.Millisecond, FullDeltaP99: 2 * time.Millisecond}})
	if rec.Decision != compact.DecisionMinor || rec.Reasons[0].Code != "minor_p99_ratio" {
		t.Fatalf("expected minor by p99, got %#v", rec)
	}
}

func TestDecideMajorPrecedence(t *testing.T) {
	m := compactManifest()
	addDelta(&m, 2, 17, 50, 400, 25, 10)
	rec := compact.Decide(m, compact.Options{})
	if rec.Decision != compact.DecisionMajor {
		t.Fatalf("expected major precedence, got %#v", rec)
	}
}

func TestDecideIgnoresMissingChangeCounts(t *testing.T) {
	m := compactManifest()
	m.Deltas = append(m.Deltas, manifest.DeltaIndexDescriptor{Generation: 2, ChangedDocsFile: "changed_docs.bin"})
	s := compact.CollectStats(m)
	if s.HasExactChangeCounts {
		t.Fatalf("expected missing exact counts: %#v", s)
	}
	rec := compact.DecideStats(s, compact.Options{})
	if rec.Decision != compact.DecisionNone {
		t.Fatalf("missing counts should not trigger changed/tombstone decision: %#v", rec)
	}
}

func TestCollectStatsTreatsZeroDeletedCountAsExactForUpsertOnlyDelta(t *testing.T) {
	m := compactManifest()
	m.Deltas = append(m.Deltas, manifest.DeltaIndexDescriptor{
		Generation:          2,
		ChangedDocsFile:     "changed_docs.bin",
		DeletedDocsFile:     "deleted_docs.bin",
		ChangedDocCount:     3,
		DeletedDocCount:     0,
		ChangedDocsChecksum: "sha256:changed",
		DeletedDocsChecksum: "sha256:deleted",
	})
	s := compact.CollectStats(m)
	if !s.HasExactChangeCounts {
		t.Fatalf("upsert-only delta has an exact zero deleted count: %#v", s)
	}
}

func TestDecideMajorByFullAge(t *testing.T) {
	rec := compact.Decide(compactManifest(), compact.Options{Observation: compact.RuntimeObservation{FullAge: 49 * time.Hour}})
	if rec.Decision != compact.DecisionMajor || rec.Reasons[0].Code != "major_full_age" {
		t.Fatalf("expected major by full age, got %#v", rec)
	}
}
