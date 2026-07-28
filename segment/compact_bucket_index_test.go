package segment

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/echoface/be_indexer/core"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// ============================================================================
// Benchmark Tests
// ============================================================================

// BenchmarkCompactBucketQuery_Uniform 均匀分布测试
func BenchmarkCompactBucketQuery_Uniform(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			idx := NewCompactBucketIndex(64)
			for i := 0; i < n; i++ {
				lo := int64(rand.Intn(1000000))
				hi := lo + int64(rand.Intn(100))
				idx.AddInterval(lo, hi, core.EntryID(i))
			}
			data, _ := idx.Build()
			reader, _ := NewCompactBucketReader(data)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := int64(rand.Intn(1000000))
				reader.Query(core.BEField("test"), q)
			}
		})
	}
}

// BenchmarkCompactBucketQuery_Skewed 偏斜分布测试（热点桶）
func BenchmarkCompactBucketQuery_Skewed(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			idx := NewCompactBucketIndex(64)
			for i := 0; i < n; i++ {
				// 80% 的区间集中在 0-1000 范围（热点桶）
				var lo int64
				if rand.Float64() < 0.8 {
					lo = int64(rand.Intn(1000))
				} else {
					lo = int64(rand.Intn(1000000))
				}
				hi := lo + int64(rand.Intn(10))
				idx.AddInterval(lo, hi, core.EntryID(i))
			}
			data, _ := idx.Build()
			reader, _ := NewCompactBucketReader(data)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 查询也遵循类似分布
				var q int64
				if rand.Float64() < 0.8 {
					q = int64(rand.Intn(1000))
				} else {
					q = int64(rand.Intn(1000000))
				}
				reader.Query(core.BEField("test"), q)
			}
		})
	}
}

// BenchmarkCompactBucketQuery_Clustered 聚类分布测试
func BenchmarkCompactBucketQuery_Clustered(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			idx := NewCompactBucketIndex(64)
			// 创建 10 个聚类中心
			centers := make([]int64, 10)
			for i := range centers {
				centers[i] = int64(rand.Intn(1000000))
			}
			for i := 0; i < n; i++ {
				center := centers[rand.Intn(len(centers))]
				lo := center + int64(rand.Intn(200)-100)
				hi := lo + int64(rand.Intn(20))
				idx.AddInterval(lo, hi, core.EntryID(i))
			}
			data, _ := idx.Build()
			reader, _ := NewCompactBucketReader(data)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				center := centers[rand.Intn(len(centers))]
				q := center + int64(rand.Intn(100)-50)
				reader.Query(core.BEField("test"), q)
			}
		})
	}
}

// ============================================================================
// RangeIndex vs CompactBucketIndex 对比
// ============================================================================

func BenchmarkRangeIndex_Query(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			// 构建 intervals
			intervals := make([]Interval, 0, n)
			for i := 0; i < n; i++ {
				lo := int64(rand.Intn(1000000))
				hi := lo + int64(rand.Intn(100))
				intervals = append(intervals, Interval{Lo: lo, Hi: hi, Entry: core.EntryID(i)})
			}
			data, _ := BuildRangeIndex(intervals)
			reader, _ := NewRangeReader(data)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := int64(rand.Intn(1000000))
				reader.MatchQuery(BlockContext{}, core.BEField("test"), q)
			}
		})
	}
}

func BenchmarkCompactBucketIndex_Query(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("intervals=%d", n), func(b *testing.B) {
			idx := NewCompactBucketIndex(64)
			for i := 0; i < n; i++ {
				lo := int64(rand.Intn(1000000))
				hi := lo + int64(rand.Intn(100))
				idx.AddInterval(lo, hi, core.EntryID(i))
			}
			data, _ := idx.Build()
			reader, _ := NewCompactBucketIndexReader(data)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				q := int64(rand.Intn(1000000))
				reader.MatchQuery(BlockContext{}, core.BEField("test"), q)
			}
		})
	}
}

// ============================================================================
// 内存使用对比
// ============================================================================

func TestMemoryUsage(t *testing.T) {
	for _, n := range []int{1000, 10000, 100000} {
		t.Run(fmt.Sprintf("intervals=%d", n), func(t *testing.T) {
			// RangeIndex 内存
			riIntervals := make([]Interval, 0, n)
			for i := 0; i < n; i++ {
				lo := int64(rand.Intn(1000000))
				hi := lo + int64(rand.Intn(100))
				riIntervals = append(riIntervals, Interval{Lo: lo, Hi: hi, Entry: core.EntryID(i)})
			}
			riData, _ := BuildRangeIndex(riIntervals)
			t.Logf("RangeIndex: %d bytes", len(riData))

			// CompactBucketIndex 内存
			cbi := NewCompactBucketIndex(64)
			for i := 0; i < n; i++ {
				lo := int64(rand.Intn(1000000))
				hi := lo + int64(rand.Intn(100))
				cbi.AddInterval(lo, hi, core.EntryID(i))
			}
			cbiData, _ := cbi.Build()
			t.Logf("CompactBucketIndex: %d bytes", len(cbiData))

			// 内存节省
			ratio := float64(len(cbiData)) / float64(len(riData))
			t.Logf("CompactBucket/RangeIndex ratio: %.2f", ratio)
		})
	}
}

// ============================================================================
// 零分配验证
// ============================================================================

func TestZeroAllocation(t *testing.T) {
	idx := NewCompactBucketIndex(64)
	for i := 0; i < 1000; i++ {
		lo := int64(rand.Intn(10000))
		hi := lo + int64(rand.Intn(100))
		idx.AddInterval(lo, hi, core.EntryID(i))
	}
	data, _ := idx.Build()
	reader, _ := NewCompactBucketReader(data)

	// 预热
	reader.Query(core.BEField("test"), 5000)

	// 记录分配
	allocs := testing.AllocsPerRun(100, func() {
		reader.Query(core.BEField("test"), 5000)
	})

	if allocs > 0 {
		t.Errorf("expected 0 allocations, got %f", allocs)
	}
}
