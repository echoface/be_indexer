package be_indexer_test

import (
	"testing"
	"time"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/manifest"
	"github.com/smartystreets/goconvey/convey"
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

func TestCollectIndexMaintenanceStats(t *testing.T) {
	convey.Convey("汇总 full 和 delta 的计数及比例", t, func() {
		m := compactManifest()
		addDelta(&m, 2, 2, 10, 100, 6, 2)
		s := be.CollectIndexMaintenanceStats(m)
		convey.So(s.FullDocCount, convey.ShouldEqual, 100)
		convey.So(s.FullBytes, convey.ShouldEqual, 1000)
		convey.So(s.FullSegments, convey.ShouldEqual, 2)
		convey.So(s.DeltaDocCount, convey.ShouldEqual, 10)
		convey.So(s.DeltaBytes, convey.ShouldEqual, 100)
		convey.So(s.DeltaSegments, convey.ShouldEqual, 2)
		convey.So(s.ChangedDocCount, convey.ShouldEqual, 6)
		convey.So(s.DeletedDocCount, convey.ShouldEqual, 2)
		convey.So(s.ChangeCountsAvailable, convey.ShouldBeTrue)
		convey.So(s.DeltaDocsRatio, convey.ShouldEqual, 0.1)
		convey.So(s.DeltaBytesRatio, convey.ShouldEqual, 0.1)
		convey.So(s.ChangedDocsRatio, convey.ShouldEqual, 0.06)
	})
}

func TestEvaluateIndexMaintenanceNoneAtThreshold(t *testing.T) {
	convey.Convey("等于阈值时不触发建议", t, func() {
		m := compactManifest()
		addDelta(&m, 2, 16, 5, 100, 5, 1)
		rec := be.EvaluateIndexMaintenance(m, be.IndexMaintenanceOptions{})
		convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionNone)
	})
}

func TestEvaluateIndexMaintenanceMinorBySegmentsAndP99(t *testing.T) {
	convey.Convey("segment 数量或 P99 延迟比超过阈值时建议评估 delta", t, func() {
		convey.Convey("一个 delta 的 17 个 segment 也能触发段数阈值", func() {
			m := compactManifest()
			addDelta(&m, 2, 17, 1, 10, 1, 0)
			rec := be.EvaluateIndexMaintenance(m, be.IndexMaintenanceOptions{})
			convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionRebuildDelta)
			convey.So(rec.Reasons, convey.ShouldHaveLength, 1)
			convey.So(rec.Reasons[0].Code, convey.ShouldEqual, "minor_delta_segments")
		})
		convey.Convey("业务传入 P99 延迟数据", func() {
			rec := be.EvaluateIndexMaintenance(compactManifest(), be.IndexMaintenanceOptions{Observation: be.IndexMaintenanceRuntimeObservation{
				FullOnlyP99: time.Millisecond, FullDeltaP99: 2 * time.Millisecond,
			}})
			convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionRebuildDelta)
			convey.So(rec.Reasons, convey.ShouldHaveLength, 1)
			convey.So(rec.Reasons[0].Code, convey.ShouldEqual, "minor_p99_ratio")
		})
	})
}

func TestEvaluateIndexMaintenanceMajorPrecedence(t *testing.T) {
	convey.Convey("同时满足两类条件时优先返回全量重建建议", t, func() {
		m := compactManifest()
		addDelta(&m, 2, 17, 50, 400, 25, 10)
		rec := be.EvaluateIndexMaintenance(m, be.IndexMaintenanceOptions{})
		convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionRebuildFull)
	})
}

func TestEvaluateIndexMaintenanceIgnoresMissingChangeCounts(t *testing.T) {
	convey.Convey("缺失变更计数时跳过依赖该计数的条件", t, func() {
		m := compactManifest()
		m.Deltas = append(m.Deltas, manifest.DeltaIndexDescriptor{Generation: 2, ChangedDocsFile: "changed_docs.bin"})
		s := be.CollectIndexMaintenanceStats(m)
		convey.So(s.ChangeCountsAvailable, convey.ShouldBeFalse)
		rec := be.EvaluateIndexMaintenanceStats(s, be.IndexMaintenanceOptions{})
		convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionNone)
	})
}

func TestCollectIndexMaintenanceStatsTreatsZeroDeletedCountAsExactForUpsertOnlyDelta(t *testing.T) {
	convey.Convey("只有 upsert 的 delta 可以有准确的零删除计数", t, func() {
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
		s := be.CollectIndexMaintenanceStats(m)
		convey.So(s.ChangeCountsAvailable, convey.ShouldBeTrue)
	})
}

func TestEvaluateIndexMaintenanceMajorByTimeSinceFullBuild(t *testing.T) {
	convey.Convey("业务传入距 full 构建完成的时长超过 48 小时", t, func() {
		rec := be.EvaluateIndexMaintenance(compactManifest(), be.IndexMaintenanceOptions{Observation: be.IndexMaintenanceRuntimeObservation{TimeSinceFullBuild: 49 * time.Hour}})
		convey.So(rec.Decision, convey.ShouldEqual, be.IndexMaintenanceDecisionRebuildFull)
		convey.So(rec.Reasons, convey.ShouldHaveLength, 1)
		convey.So(rec.Reasons[0].Code, convey.ShouldEqual, "major_full_age")
	})
}
