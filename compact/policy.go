package compact

import (
	"time"

	"github.com/echoface/be_indexer/manifest"
)

// Decision is the recommended compaction action.
type Decision string

const (
	DecisionNone  Decision = "none"
	DecisionMinor Decision = "minor"
	DecisionMajor Decision = "major"
)

// Stats is a manifest-level static summary used for compaction decisions.
type Stats struct {
	Generation     uint64
	FullGeneration uint64
	FullDocCount   uint64
	FullSegments   int
	FullBytes      uint64

	DeltaCount    int
	DeltaSegments int
	DeltaDocCount uint64
	DeltaBytes    uint64

	ChangedDocCount uint64
	DeletedDocCount uint64

	DeltaDocsRatio   float64
	DeltaBytesRatio  float64
	ChangedDocsRatio float64
	TombstoneRatio   float64

	HasExactChangeCounts bool
}

// Policy defines threshold values. A threshold is triggered only when value > threshold.
type Policy struct {
	MinorDeltaSegmentThreshold int
	MinorDeltaDocsRatio        float64
	MinorDeltaBytesRatio       float64
	MinorChangedDocsRatio      float64
	MinorP99Ratio              float64

	MajorChangedDocsRatio float64
	MajorDeltaBytesRatio  float64
	MajorTombstoneRatio   float64
	MajorFullAge          time.Duration
}

// RuntimeObservation contains optional runtime measurements supplied by caller.
type RuntimeObservation struct {
	FullOnlyP99  time.Duration
	FullDeltaP99 time.Duration
	FullAge      time.Duration
}

// Options controls compaction decision.
type Options struct {
	Policy      Policy
	Observation RuntimeObservation
}

// Reason explains why a decision was made.
type Reason struct {
	Code      string
	Value     float64
	Threshold float64
}

// Recommendation is the result of compact decision.
type Recommendation struct {
	Decision Decision
	Stats    Stats
	Reasons  []Reason
}

// DefaultPolicy returns the default thresholds from the design doc.
func DefaultPolicy() Policy {
	return Policy{
		MinorDeltaSegmentThreshold: 16,
		MinorDeltaDocsRatio:        0.05,
		MinorDeltaBytesRatio:       0.10,
		MinorChangedDocsRatio:      0.05,
		MinorP99Ratio:              1.20,

		MajorChangedDocsRatio: 0.20,
		MajorDeltaBytesRatio:  0.30,
		MajorTombstoneRatio:   0.20,
		MajorFullAge:          48 * time.Hour,
	}
}

// CollectStats summarizes a manifest without reading segment or sidecar files.
func CollectStats(m manifest.Manifest) Stats {
	s := Stats{Generation: m.Generation, FullGeneration: m.Full.Generation}
	s.FullSegments = len(m.Full.Segments)
	for _, seg := range m.Full.Segments {
		s.FullDocCount += seg.DocCount
		s.FullBytes += seg.Size
	}

	s.DeltaCount = len(m.Deltas)
	s.HasExactChangeCounts = true
	for _, delta := range m.Deltas {
		s.DeltaSegments += len(delta.Segments)
		for _, seg := range delta.Segments {
			s.DeltaDocCount += seg.DocCount
			s.DeltaBytes += seg.Size
		}
		s.ChangedDocCount += delta.ChangedDocCount
		s.DeletedDocCount += delta.DeletedDocCount
		if delta.ChangedDocsFile != "" && delta.ChangedDocCount == 0 {
			s.HasExactChangeCounts = false
		}
		// BuildDeltaIndexDir always writes deleted_docs.bin, including the valid
		// upsert-only case where DeletedDocCount is exactly zero. Treat a zero
		// deleted count as missing only when the paired changed count is also absent.
		if delta.DeletedDocsFile != "" && delta.DeletedDocCount == 0 && delta.ChangedDocCount == 0 {
			s.HasExactChangeCounts = false
		}
	}
	s.DeltaDocsRatio = ratio(s.DeltaDocCount, s.FullDocCount)
	s.DeltaBytesRatio = ratio(s.DeltaBytes, s.FullBytes)
	s.ChangedDocsRatio = ratio(s.ChangedDocCount, s.FullDocCount)
	s.TombstoneRatio = ratio(s.DeletedDocCount, s.ChangedDocCount)
	return s
}

// Decide computes a compaction recommendation from a manifest.
func Decide(m manifest.Manifest, opts Options) Recommendation {
	return DecideStats(CollectStats(m), opts)
}

// DecideStats computes a compaction recommendation from pre-collected stats.
func DecideStats(stats Stats, opts Options) Recommendation {
	policy := opts.Policy
	if policy == (Policy{}) {
		policy = DefaultPolicy()
	}
	majorReasons := majorReasons(stats, policy, opts.Observation)
	if len(majorReasons) > 0 {
		return Recommendation{Decision: DecisionMajor, Stats: stats, Reasons: majorReasons}
	}
	minorReasons := minorReasons(stats, policy, opts.Observation)
	if len(minorReasons) > 0 {
		return Recommendation{Decision: DecisionMinor, Stats: stats, Reasons: minorReasons}
	}
	return Recommendation{Decision: DecisionNone, Stats: stats}
}

func majorReasons(stats Stats, policy Policy, obs RuntimeObservation) []Reason {
	var reasons []Reason
	if stats.HasExactChangeCounts && stats.ChangedDocsRatio > policy.MajorChangedDocsRatio {
		reasons = append(reasons, Reason{Code: "major_changed_docs_ratio", Value: stats.ChangedDocsRatio, Threshold: policy.MajorChangedDocsRatio})
	}
	if stats.DeltaBytesRatio > policy.MajorDeltaBytesRatio {
		reasons = append(reasons, Reason{Code: "major_delta_bytes_ratio", Value: stats.DeltaBytesRatio, Threshold: policy.MajorDeltaBytesRatio})
	}
	if stats.HasExactChangeCounts && stats.TombstoneRatio > policy.MajorTombstoneRatio {
		reasons = append(reasons, Reason{Code: "major_tombstone_ratio", Value: stats.TombstoneRatio, Threshold: policy.MajorTombstoneRatio})
	}
	if policy.MajorFullAge > 0 && obs.FullAge > policy.MajorFullAge {
		reasons = append(reasons, Reason{Code: "major_full_age", Value: float64(obs.FullAge), Threshold: float64(policy.MajorFullAge)})
	}
	return reasons
}

func minorReasons(stats Stats, policy Policy, obs RuntimeObservation) []Reason {
	var reasons []Reason
	if stats.DeltaSegments > policy.MinorDeltaSegmentThreshold {
		reasons = append(reasons, Reason{Code: "minor_delta_segments", Value: float64(stats.DeltaSegments), Threshold: float64(policy.MinorDeltaSegmentThreshold)})
	}
	if stats.DeltaDocsRatio > policy.MinorDeltaDocsRatio {
		reasons = append(reasons, Reason{Code: "minor_delta_docs_ratio", Value: stats.DeltaDocsRatio, Threshold: policy.MinorDeltaDocsRatio})
	}
	if stats.DeltaBytesRatio > policy.MinorDeltaBytesRatio {
		reasons = append(reasons, Reason{Code: "minor_delta_bytes_ratio", Value: stats.DeltaBytesRatio, Threshold: policy.MinorDeltaBytesRatio})
	}
	if stats.HasExactChangeCounts && stats.ChangedDocsRatio > policy.MinorChangedDocsRatio {
		reasons = append(reasons, Reason{Code: "minor_changed_docs_ratio", Value: stats.ChangedDocsRatio, Threshold: policy.MinorChangedDocsRatio})
	}
	if obs.FullOnlyP99 > 0 && obs.FullDeltaP99 > 0 && policy.MinorP99Ratio > 0 {
		p99Ratio := float64(obs.FullDeltaP99) / float64(obs.FullOnlyP99)
		if p99Ratio > policy.MinorP99Ratio {
			reasons = append(reasons, Reason{Code: "minor_p99_ratio", Value: p99Ratio, Threshold: policy.MinorP99Ratio})
		}
	}
	return reasons
}

func ratio(n, d uint64) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
