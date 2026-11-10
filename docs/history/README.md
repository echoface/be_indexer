# 历史设计记录

本目录归档**已完成或已废弃**的设计、评审与实施计划，用于追溯当时的约束、取舍和验证过程。

> 这些文档保留原始结论，不保证与当前实现一致。当前 API 与行为请以代码、根 [README](../../README.md) 和 [当前文档](../README.md) 为准。

## 目录

### Geo 设计演进

| 文档 | 说明 |
| :--- | :--- |
| [geo-radius-design.md](geo/geo-radius-design.md) | Geo 半径检索的早期完整方案 |
| [geo-design-v2.md](geo/geo-design-v2.md) | 终版方案（自定义 Geo 容器） |
| [geo-design-v3.md](geo/geo-design-v3.md) | 终版方案（proximityhash 覆盖 cell + 纯 term 索引） |

### 实施计划

| 文档 | 说明 |
| :--- | :--- |
| [2026-07-18 geo-proximityhash-term-encoder.md](plans/2026-07-18-geo-proximityhash-term-encoder.md) | 用 proximityhash 纯 term 方案替换自定义 Geo 容器 |
| [2026-07-25 remove-encodedquery-kind.md](plans/2026-07-25-remove-encodedquery-kind.md) | 移除 `EncodedQuery.Kind`，解耦编码器与容器路由 |
| [2026-07-25 unify-container-matchquery.md](plans/2026-07-25-unify-container-matchquery.md) | 统一 `IndexReader.MatchQuery` 容器接口 |
| [2026-09-12 unify-segment-builders.md](plans/2026-09-12-unify-segment-builders.md) | 合并内存/溢写构建器为单一 `SegmentBuilder` |

### 设计规格

| 文档 | 说明 |
| :--- | :--- |
| [2026-07-25 mph-dict-container-design.md](specs/2026-07-25-mph-dict-container-design.md) | `mph_dict` 容器设计 |
| [2026-07-28 compact-bucket-index-analysis.md](specs/2026-07-28-compact-bucket-index-analysis.md) | CompactBucketIndex 与 RangeIndex 的对比分析 |
