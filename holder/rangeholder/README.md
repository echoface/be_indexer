# RangeHolder 模块文档

## 简介
本模块提供了两种处理数值范围查询（Range Query）的 `EntriesHolder` 实现。它们用于支持如 `age > 18`、`price < 100`、`time between [t1, t2]` 等查询场景。

## 实现方案对比

### 1. RangeHolder (ext_range)
*   **实现原理**：基于链表和区间分裂（Interval Splitting）。将所有注册的范围进行切分，形成互不重叠的原子区间。
*   **优点**：查询逻辑简单，对于范围重叠较少、数据量较小的场景，查询速度极快（二分查找）。
*   **缺点**：
    *   **区间爆炸**：当存在大量重叠范围时，区间数量呈 $O(N^2)$ 增长。
    *   **构建极慢**：插入新文档时需要分裂现有区间，构建耗时极高。
    *   **内存占用高**：大量的链表节点和微小区间占用大量内存。

### 2. OptimizedRangeHolder (optimized_range)
*   **实现原理**：采用 **混合存储策略 (Hybrid Storage)**，结合了 **FlatMap** 和 **动态线段树 (Dynamic Segment Tree)**。
    *   **混合存储**：
        *   **EQ 查询 (精确匹配)**：使用 **FlatMap (Sorted Keys + Compressed Posting List)** 存储。查询时通过二分查找 Key，直接定位并解压对应的文档 ID 列表。
        *   **Range 查询 (范围匹配)**：使用 **动态线段树** + **坐标压缩**。只在需要的路径上动态创建节点，避免了全量树的内存开销。
    *   **压缩技术**：
        *   **Varint Delta**：对 EQ 的 Posting List 进行差分 Varint 编码，大幅减少内存占用。
        *   **坐标压缩**：将稀疏的范围边界映射为连续整数索引。
*   **优点**：
    *   **构建极快**：避免了区间分裂，构建复杂度降低到 $O(N \log N)$。
    *   **内存极低**：通过压缩和动态树，内存占用相比旧版降低 99% 以上。
    *   **查询均衡**：混合存储策略使得高频的 EQ 查询也能保持极高的性能（~50μs），同时支持复杂的范围查询。
*   **缺点**：
    *   **纯范围查询稍慢**：相比旧版的直接二分查找，线段树查询需要递归和合并，有一定开销（仍在亚毫秒级）。

## 性能对比 (Benchmark)

基于 10,000 个文档的测试结果（Mac M1 Pro）：

| 指标 | RangeHolder (ext_range) | OptimizedRangeHolder (optimized_range) | 差异 |
| :--- | :--- | :--- | :--- |
| **构建耗时** | ~3.22s | **~15ms** | **提升约 213 倍** 🚀 |
| **内存占用** | ~3.38GB | **~11.5MB** | **降低 99.6%** 📉 |
| **混合查询耗时** (90% EQ) | ~12μs | **~57μs** | 增加约 4-5 倍 (仍极快) |
| **纯范围查询耗时** | ~102μs | ~693μs | 增加约 6-7 倍 |

> **测试结论**：
> 1. `OptimizedRangeHolder` 彻底解决了旧版在构建速度和内存占用上的瓶颈。
> 2. 虽然查询延迟略有增加，但通过混合存储优化，混合场景下的查询延迟控制在 **~60μs** 级别，完全满足高性能在线服务的需求。
> 3. 对于绝大多数生产环境，**强烈推荐使用优化版**。

## 选型建议

*   **默认推荐**：使用 `OptimizedRangeHolder` (`optimized_range`)。
    *   适用于：绝大多数场景，特别是文档数量多 (>1000)、范围重叠多、关注内存和构建效率的场景。
*   **特殊场景**：使用 `RangeHolder` (`ext_range`)。
    *   适用于：文档数量极少（< 1000）、范围几乎不重叠、且对查询延迟有极致要求（< 20μs）的离线静态数据场景。

## 使用指南

### 1. 注册
该包在 `init()` 函数中会自动注册两种 Holder，无需手动干预。

### 2. 配置 IndexerBuilder
在创建 `IndexerBuilder` 时，通过 `ConfigField` 指定字段使用的 Holder 类型：

```go
builder := be_indexer.NewCompactIndexerBuilder()

// 使用优化版 (推荐)
builder.ConfigField("age", be_indexer.FieldOption{
    Container: "optimized_range",
})

// 或者使用旧版
// builder.ConfigField("price", be_indexer.FieldOption{
//     Container: be_indexer.HolderNameExtendRange, // "ext_range"
// })
```

### 3. 查询示例
支持 `EQ` (=), `LT` (<), `GT` (>), `Between` 等操作符：

```go
// 1. 索引构建
doc := be_indexer.NewDocument(1)
// 范围: age < 30
doc.AddConjunction(be_indexer.NewConjunction().LessThan("age", 30))
// 精确值: age = 25 (内部优化为 EQ 存储)
doc.AddConjunction(be_indexer.NewConjunction().Include("age", []int{25}))
builder.AddDocument(doc)
// ...

// 2. 查询
// 查询 age = 10 的文档 (会匹配 age < 30 的规则)
ids, _ := indexer.Retrieve(be_indexer.Assignments{
    "age": 10,
})
```

## 目录结构
*   `optimized_range_holder.go`: 优化版实现 (混合存储：FlatMap + 线段树)
*   `term_ext_range_holder.go`: 旧版实现 (区间分裂)
*   `DESIGN.md`: 详细设计文档 (早期设计，部分实现已更新)
*   `range_holder_benchmark_test.go`: 性能测试代码
