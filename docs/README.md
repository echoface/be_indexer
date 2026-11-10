# be_indexer 文档

本目录是项目文档的主入口，按**使用 → 架构 → 设计 → 历史**组织。项目根目录的 [README](../README.md) 提供概览，具体细节以本目录和代码为准。

## 使用指南

面向业务接入方，回答“怎么用对、怎么用稳”。

| 文档 | 内容 |
| :--- | :--- |
| [快速开始](guide/quick-start.md) | 从 Schema 定义到构建、查询的最小可运行流程 |
| [最佳实践](guide/best-practices.md) | 规则建模、容器选型、一个 full + 一个累计 delta 的生产链路、热更新与反模式 |
| [索引合并与全量发布评估](guide/best-practices.md#10-索引合并与全量发布评估) | `api_maintenance.go` 的业务参考用途、推荐发布方式和调用方职责 |
| [示例集合](guide/examples.md) | 广告定向、AC 自动机、Exclude/Z-Entry、full + delta 等场景配方 |
| [API 参考](guide/api-reference.md) | 根包 `api_*.go` 的分组 API 与容器扩展接口 |

## 架构与索引

描述**当前实现**，不包含历史方案。

| 文档 | 内容 |
| :--- | :--- |
| [架构与实现](architecture/overview.md) | 包边界、ID 编码、构建与查询路径、Segment 格式、发布与加载 |
| [索引类型](architecture/indexes/README.md) | 可插拔容器的选型、复杂度与共享存储模型 |
| [default](architecture/indexes/default.md) | FlatDict 等值索引 |
| [mph_dict](architecture/indexes/mph_dict.md) | CHD 最小完美哈希 |
| [fst_dict](architecture/indexes/fst_dict.md) | FST 前缀/后缀压缩字典 |
| [ac_matcher](architecture/indexes/ac_matcher.md) | Aho-Corasick 多模式匹配 |
| [ext_range](architecture/indexes/ext_range.md) | 数值区间索引 |
| [proximitygeo](architecture/indexes/proximitygeo.md) | 基于默认索引的 Geo 半径检索编码器 |

## 设计文档

当前有效的独立设计说明。

| 文档 | 内容 |
| :--- | :--- |
| [Exact Top K 与独立分数 sidecar](design/topk.md) | 全局 Top K 检索、分数快照、剪枝与正确性 |

## 历史记录

归档的设计、评审与实施计划，仅用于追溯决策过程；**当前 API 与行为以代码、根 README 和本目录当前文档为准**。

| 文档 | 内容 |
| :--- | :--- |
| [历史记录说明](history/README.md) | 归档范围与阅读方式 |
| [Geo 设计](history/geo/geo-radius-design.md) | Geo 半径检索的早期方案与两版演进 |
| [实施计划](history/plans/) | 已完成的重构/功能实施计划 |
| [设计规格](history/specs/) | 容器与分析类设计规格 |

## 参考资料

论文、设计图与背景笔记保留在 [`static/design/`](../static/design/)：

- [VLDB 09 论文](../static/design/vldb09-indexing.pdf)
- [布尔表达式运算符说明](../static/design/boolean_operator.md)
- [倒排链压缩与块式迭代](../static/design/inverted_index_compression.md)
- [Yahoo Indexer 设计图](../static/design/yahoo-indexer.png)
