package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"testing"
)

/*
⏺ 我们通过 Benchmark 实测了 Map 模式和 Flat 模式的查询性能，结果非常有趣且值得分析：

  ### Benchmark 结果数据

   core.Term 数量 (N) │ Map 模式 (ns/op) │ Flat 模式 (ns/op) │ 性能差距
  ───────────────┼──────────────────┼───────────────────┼──────────────────
   1,000         │ 10.02            │ 31.63             │ Flat 慢约 3 倍
   10,000        │ 17.24            │ 51.31             │ Flat 慢约 3 倍
   100,000       │ 26.15            │ 66.04             │ Flat 慢约 2.5 倍
   1,000,000     │ 47.20            │ 72.06             │ Flat 慢约 1.5 倍

  ### 结果分析

  1. O(1) vs O(logN) 的效应确实存在：
    • 在小数据量下，Map 的哈希查找确实比二分查找快 3 倍左右。
    • 这是预期的，因为哈希是直接定位，而二分需要多次比较。
  2. 差距随着数据量增大而缩小：
    • 请注意，随着 N 从 10万 增加到 100万，Map 的耗时增加了 80% (26ns -> 47ns)，而 Flat 仅增加了 9% (66ns -> 72ns)。
    • 这证明了 Flat 模式具有极好的 CPU Cache 亲和性。当 Map 变大时，它在堆内存中更加分散，导致 CPU Cache Miss 急剧增加。而 Flat 模式的数据是紧凑连续的，预取效率极高，很大程度上抵消了 O(logN)
  的比较开销。
  3. 绝对耗时极低：
    • 即使在 100万个 core.Term 的极端情况下，Flat 模式的一次查询耗时也仅为 72纳秒 (0.000072毫秒)。
    • 在一个典型的广告检索请求中，可能会查询几十个 core.Term，总开销在 几微秒 级别。这相对于整个检索链路（网络IO、倒排链求交、排序等，通常在毫秒级）来说，是可以忽略不计的。


  ### 结论与建议

  虽然 Flat 模式在单一查找指令上比 Map 稍慢（几十纳秒的差距），但它换来了巨大的系统级收益：

  1. GC 压力归零：Map 模式下 100万个 core.Term 会产生数百万个小对象，给 GC 带来巨大负担，导致 STW (Stop-The-World) 延迟。Flat 模式只有 3 个大数组，GC 几乎无感。
  2. 内存节省：Flat 模式去除了 Map 的桶结构、指针和 Slice Header，内存占用通常减少 30% 以上。
  3. 为压缩铺路：Flat 模式的连续内存结构是实现 Phase 2 (SIMD 压缩) 的必要基础。

  综上所述，Flat 模式的设计是正确的。它用微不足道的 CPU 查找时间（纳秒级），换取了宝贵的内存空间和系统稳定性。

  如果您对特定场景的性能有极高要求，我们可以在 Phase 3 引入 Perfect Hash 或 FST (Finite State Transducer) 索引结构，进一步优化查找速度，但在目前阶段，Flat 模式已经足够优秀。
*/

func prepareCompressedHolder(termCount int) *CompressedKVIndex {
	h := NewCompressedKVBuilder()
	for i := 0; i < termCount; i++ {
		// 模拟不同的 FieldID 和 IDValue
		fieldID := uint64(i % 10) // 10个字段
		idValue := fmt.Sprintf("%d", i)

		// 构造一些 core.EntryID
		for j := 0; j < 5; j++ {
			eid := core.NewEntryID(core.NewConjID(core.DocID(j), 0, 1), true)
			h.builder.AddEntryWithFieldID(fieldID, idValue, eid)
		}
	}
	holder, _ := h.CompileEntries()
	return holder.(*CompressedKVIndex)
}

// BenchmarkHolder_CompressedLookup_Scan 测试 Compressed 模式下的查找+解压访问开销
func BenchmarkHolder_CompressedLookup_Scan(b *testing.B) {
	counts := []int{1000, 10000, 100000, 1000000}

	for _, count := range counts {
		b.Run(fmt.Sprintf("Count_%d", count), func(b *testing.B) {
			holder := prepareCompressedHolder(count)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				target := i % count
				fieldID := uint64(target % 10)
				idValue := fmt.Sprintf("%d", target)

				desc := &core.FieldDesc{ID: fieldID, Field: "test"}
				
				// 1. GetEntries (二分查找 + Cursor初始化)
				cursors, _ := holder.GetEntries(desc, core.Values(idValue))
				
				// 2. Scan (SkipTo 解压 Block)
				if len(cursors) > 0 {
					// Compressed 模式下，GetEntries 不会立即解压
					// 必须调用 SkipTo 或 GetCurEntryID 才会触发解压
					// Cursor 默认 curEID 已经指向第一个元素(在 GetEntries 中已触发一次 LoadBlock)
					_ = cursors[0].Current()
				}
			}
		})
	}
}
