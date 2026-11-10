package be_indexer

import "time"

// IndexMaintenanceDecision 是供业务参考的索引合并或全量重建建议。
// 本文件只做统计和阈值评估；数据读取、构建、发布和 Reload 由业务安排。
// 业务推荐使用一个 full 和一个累计 delta，每个索引均可包含多个 segment。
type IndexMaintenanceDecision string

const (
	// IndexMaintenanceDecisionNone 表示没有触发配置的评估阈值。
	IndexMaintenanceDecisionNone IndexMaintenanceDecision = "none"
	// IndexMaintenanceDecisionRebuildDelta 建议业务评估是否合并或重新构建 delta。
	IndexMaintenanceDecisionRebuildDelta IndexMaintenanceDecision = "minor"
	// IndexMaintenanceDecisionRebuildFull 建议业务评估是否重新构建并发布 full。
	IndexMaintenanceDecisionRebuildFull IndexMaintenanceDecision = "major"
)

// IndexMaintenanceStats 保存 Manifest 中的静态统计，供业务评估索引合并或重建。
// 多个 delta 的计数直接累加，不按 DocID 跨 delta 去重；不代表最终可见文档数。
type IndexMaintenanceStats struct {
	Generation     uint64
	FullGeneration uint64
	FullDocCount   uint64
	FullSegments   int
	FullBytes      uint64

	DeltaCount    int // delta 索引数量。
	DeltaSegments int // 所有 delta 包含的 segment 总数。
	DeltaDocCount uint64
	DeltaBytes    uint64

	ChangedDocCount uint64
	DeletedDocCount uint64

	DeltaDocsRatio        float64 // DeltaDocCount / FullDocCount。
	DeltaBytesRatio       float64 // DeltaBytes / FullBytes。
	ChangedDocsRatio      float64 // ChangedDocCount / FullDocCount。
	DeletedToChangedRatio float64 `json:"TombstoneRatio"` // DeletedDocCount / ChangedDocCount。

	// ChangeCountsAvailable 表示未发现缺失的变更/删除计数，不表示跨 delta 去重。
	ChangeCountsAvailable bool `json:"HasExactChangeCounts"`
}

// IndexMaintenancePolicy 定义评估阈值，仅实际值严格大于阈值时触发。
// 修改部分阈值时应先调用 DefaultIndexMaintenancePolicy；只有整个零值结构体才使用默认策略。
type IndexMaintenancePolicy struct {
	MinorDeltaSegmentThreshold int
	MinorDeltaDocsRatio        float64
	MinorDeltaBytesRatio       float64
	MinorChangedDocsRatio      float64
	MinorP99Ratio              float64

	MajorChangedDocsRatio float64
	MajorDeltaBytesRatio  float64
	MajorTombstoneRatio   float64
	// MajorFullAge 是距当前 full 构建完成的时长阈值；<= 0 时不评估此项。
	MajorFullAge time.Duration
}

// IndexMaintenanceRuntimeObservation 保存调用方提供的可选运行指标，本库不自动采集。
type IndexMaintenanceRuntimeObservation struct {
	FullOnlyP99  time.Duration // 只查询 full 时，请求耗时的第 99 百分位数。
	FullDeltaP99 time.Duration // 查询 full + delta 时，请求耗时的第 99 百分位数。
	// TimeSinceFullBuild 是评估时间减去当前 full 的构建完成时间，如 time.Since(fullBuiltAt)。
	// 构建完成时间由业务记录，不能用每次 delta 发布都会更新的 Manifest 时间代替。
	TimeSinceFullBuild time.Duration `json:"FullAge"`
}

// IndexMaintenanceOptions 配置评估策略及业务提供的运行指标。
type IndexMaintenanceOptions struct {
	Policy      IndexMaintenancePolicy
	Observation IndexMaintenanceRuntimeObservation
}

// IndexMaintenanceReason 记录触发的条件、实际值和阈值。
// 比例为无量纲数值；major_full_age 的 Value 和 Threshold 单位为纳秒。
type IndexMaintenanceReason struct {
	Code      string
	Value     float64
	Threshold float64
}

// IndexMaintenanceRecommendation 是业务评估参考，不会自动触发合并、重建或发布任务。
type IndexMaintenanceRecommendation struct {
	Decision IndexMaintenanceDecision
	Stats    IndexMaintenanceStats
	Reasons  []IndexMaintenanceReason
}

// DefaultIndexMaintenancePolicy 返回默认参考阈值，业务应根据查询成本和构建成本调整。
func DefaultIndexMaintenancePolicy() IndexMaintenancePolicy {
	return IndexMaintenancePolicy{
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

// CollectIndexMaintenanceStats 只汇总传入的 Manifest，不读取 segment 或 sidecar 文件。
// 各比例在分母为零时记为零；输入元数据的有效性由调用方保证。
func CollectIndexMaintenanceStats(m Manifest) IndexMaintenanceStats {
	s := IndexMaintenanceStats{Generation: m.Generation, FullGeneration: m.Full.Generation}
	s.FullSegments = len(m.Full.Segments)
	for _, seg := range m.Full.Segments {
		s.FullDocCount += seg.DocCount
		s.FullBytes += seg.Size
	}

	s.DeltaCount = len(m.Deltas)
	s.ChangeCountsAvailable = true
	for _, delta := range m.Deltas {
		s.DeltaSegments += len(delta.Segments)
		for _, seg := range delta.Segments {
			s.DeltaDocCount += seg.DocCount
			s.DeltaBytes += seg.Size
		}
		s.ChangedDocCount += delta.ChangedDocCount
		s.DeletedDocCount += delta.DeletedDocCount
		if delta.ChangedDocsFile != "" && delta.ChangedDocCount == 0 {
			s.ChangeCountsAvailable = false
		}
		// DeltaIndexBuilder always writes deleted_docs.bin, including the valid
		// upsert-only case where DeletedDocCount is exactly zero. Treat a zero
		// deleted count as missing only when the paired changed count is also absent.
		if delta.DeletedDocsFile != "" && delta.DeletedDocCount == 0 && delta.ChangedDocCount == 0 {
			s.ChangeCountsAvailable = false
		}
	}
	s.DeltaDocsRatio = compactRatio(s.DeltaDocCount, s.FullDocCount)
	s.DeltaBytesRatio = compactRatio(s.DeltaBytes, s.FullBytes)
	s.ChangedDocsRatio = compactRatio(s.ChangedDocCount, s.FullDocCount)
	s.DeletedToChangedRatio = compactRatio(s.DeletedDocCount, s.ChangedDocCount)
	return s
}

// EvaluateIndexMaintenance 根据 Manifest 和可选运行指标，评估是否合并 delta 或重建并发布 full。
// 它只返回建议及原因，不执行 I/O、调度任务或修改索引。
// 业务推荐一个 full + 一个累计 delta；阈值超过时是否执行任务由业务决定。
func EvaluateIndexMaintenance(m Manifest, opts IndexMaintenanceOptions) IndexMaintenanceRecommendation {
	return EvaluateIndexMaintenanceStats(CollectIndexMaintenanceStats(m), opts)
}

// EvaluateIndexMaintenanceStats 根据已有统计返回建议，major 条件优先于 minor。
// 调用方需填好比例和 ChangeCountsAvailable，本函数不从计数重新计算这些字段。
func EvaluateIndexMaintenanceStats(stats IndexMaintenanceStats, opts IndexMaintenanceOptions) IndexMaintenanceRecommendation {
	policy := opts.Policy
	if policy == (IndexMaintenancePolicy{}) {
		policy = DefaultIndexMaintenancePolicy()
	}
	majorReasons := majorCompactReasons(stats, policy, opts.Observation)
	if len(majorReasons) > 0 {
		return IndexMaintenanceRecommendation{Decision: IndexMaintenanceDecisionRebuildFull, Stats: stats, Reasons: majorReasons}
	}
	minorReasons := minorCompactReasons(stats, policy, opts.Observation)
	if len(minorReasons) > 0 {
		return IndexMaintenanceRecommendation{Decision: IndexMaintenanceDecisionRebuildDelta, Stats: stats, Reasons: minorReasons}
	}
	return IndexMaintenanceRecommendation{Decision: IndexMaintenanceDecisionNone, Stats: stats}
}

func majorCompactReasons(stats IndexMaintenanceStats, policy IndexMaintenancePolicy, obs IndexMaintenanceRuntimeObservation) []IndexMaintenanceReason {
	var reasons []IndexMaintenanceReason
	if stats.ChangeCountsAvailable && stats.ChangedDocsRatio > policy.MajorChangedDocsRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "major_changed_docs_ratio", Value: stats.ChangedDocsRatio, Threshold: policy.MajorChangedDocsRatio})
	}
	if stats.DeltaBytesRatio > policy.MajorDeltaBytesRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "major_delta_bytes_ratio", Value: stats.DeltaBytesRatio, Threshold: policy.MajorDeltaBytesRatio})
	}
	if stats.ChangeCountsAvailable && stats.DeletedToChangedRatio > policy.MajorTombstoneRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "major_tombstone_ratio", Value: stats.DeletedToChangedRatio, Threshold: policy.MajorTombstoneRatio})
	}
	if policy.MajorFullAge > 0 && obs.TimeSinceFullBuild > policy.MajorFullAge {
		reasons = append(reasons, IndexMaintenanceReason{Code: "major_full_age", Value: float64(obs.TimeSinceFullBuild), Threshold: float64(policy.MajorFullAge)})
	}
	return reasons
}

func minorCompactReasons(stats IndexMaintenanceStats, policy IndexMaintenancePolicy, obs IndexMaintenanceRuntimeObservation) []IndexMaintenanceReason {
	var reasons []IndexMaintenanceReason
	if stats.DeltaSegments > policy.MinorDeltaSegmentThreshold {
		reasons = append(reasons, IndexMaintenanceReason{Code: "minor_delta_segments", Value: float64(stats.DeltaSegments), Threshold: float64(policy.MinorDeltaSegmentThreshold)})
	}
	if stats.DeltaDocsRatio > policy.MinorDeltaDocsRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "minor_delta_docs_ratio", Value: stats.DeltaDocsRatio, Threshold: policy.MinorDeltaDocsRatio})
	}
	if stats.DeltaBytesRatio > policy.MinorDeltaBytesRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "minor_delta_bytes_ratio", Value: stats.DeltaBytesRatio, Threshold: policy.MinorDeltaBytesRatio})
	}
	if stats.ChangeCountsAvailable && stats.ChangedDocsRatio > policy.MinorChangedDocsRatio {
		reasons = append(reasons, IndexMaintenanceReason{Code: "minor_changed_docs_ratio", Value: stats.ChangedDocsRatio, Threshold: policy.MinorChangedDocsRatio})
	}
	if obs.FullOnlyP99 > 0 && obs.FullDeltaP99 > 0 && policy.MinorP99Ratio > 0 {
		p99Ratio := float64(obs.FullDeltaP99) / float64(obs.FullOnlyP99)
		if p99Ratio > policy.MinorP99Ratio {
			reasons = append(reasons, IndexMaintenanceReason{Code: "minor_p99_ratio", Value: p99Ratio, Threshold: policy.MinorP99Ratio})
		}
	}
	return reasons
}

func compactRatio(n, d uint64) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}
