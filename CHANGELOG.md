# ChangeLog

All notable changes to this project will be documented in this file.

## [Unreleased] - 2026-11-06

### Added

#### segmentindex 增量 Segment、Tombstone 与 Compaction（Phase 4）

在 `segmentindex` 引入 `Store` 编排层：Delta Segment 增量写入、序列号 Tombstone 的 Delete / Update、后台 Segment Merge（Compaction）、限速与失败恢复。

1. **Delta Segment 与 Store**
   - `Store` 将增量写入积累在 pending buffer 中，`Flush`（或 `StoreOptions.FlushDocs` 阈值）封存为不可变 `Delta`；批量 `AddDocuments` 是原子的，失败批次整体回滚
   - 每次写入（Add / Delete / Update / Flush / Compact）发布新的不可变视图；`StoreSnapshot` 在创建时固定视图，之后的删除、追加、合并都不会影响进行中的查询（查询开始时固定 Snapshot）
   - `StoreSnapshot.RetrieveAssignments` / `Retrieve` 为快照读；`Store` 上的同名方法是一次性便捷查询

2. **序列号 Tombstone：Delete / Update**
   - 每个 Store 维护单调递增 `seq`：文档添加记录 `addSeq`，每次 `Delete(ids...)` 调用取一个 `seq` 并记录 `tombstones[id] = max(seq)`；`id@addSeq` 版本在 `tombSeq > addSeq` 时死亡
   - 因此 `Update(id, doc)` = Delete + Add 天然成立：新版本 `addSeq` 大于 tombstone 序列号保持存活；删除后重新添加同样有效
   - 一个 doc 的多个版本（多个 delta）各自携带 addSeq，独立参与 tombstone 判定
   - Compaction 后可回收失效 tombstone：当 id 不在 pending buffer 且所有剩余版本的 addSeq 均不小于 tombstone 序列号时删除该 tombstone

3. **后台 Segment Merge（Compaction）**
   - `Compact(ctx)` / `StartBackgroundCompaction()` 将多个 Delta 合并重写：物理剔除 tombstoned 版本（postings 与 liveDocs）、丢弃全灭的 group 与空 term、合并相同 Field/Term postings 并重建统计信息
   - **Group 合并规则**：仅当两个 group 的 `groupSignature`（每字段 include/exclude 形状 + 全部 range 谓词边界）一致**且 doc id 集合互不相交**时才合并。不相交约束保证：同一 doc 的多个 conjunction 不会被并进同一 group（DNF OR 边界不跨字段误报），同一 doc 的多个版本不会共用一个 addSeq
   - fallback 语义改由表达式形状（`fieldShape.hasIncl`）决定而非合并后的 postings，保证合并 group 保持源 conjunction 的空 include 集回退行为
   - 后台 worker 通过 `CompactionOptions.MinDeltas` 触发、`MaxDeltasPerRound` 限制单轮合并规模，失败按 `MaxAttempts`/`RetryBackoff` 线性退避重试，`Close` 统一停止

4. **限速与失败恢复**
   - 新增 `RateLimiter` 接口与 `TokenBucket` 实现（`CompactionOptions.RateBytesPerSecond/RateBurst`），合并按处理的 doc version 数量计费排队，避免后台合并挤压在线查询；`StoreOptions.Limiter` 可注入自定义实现
   - 失败恢复：合并全程在 store 锁外构建临时 bucket，仅成功后持锁发布；失败（限速器/ctx/构建错误）时在线版本保持不变、临时产物直接丢弃，`StoreStats` 记录 `CompactionFailures`/`LastCompactionError`

**行为变更：**

- `Index.Retrieve` 不再原地改写调用方的 `planner.Query`（改为私有副本加前缀）；旧实现在同一 query 复用于多个 delta 时会重复加前缀导致漏检
- `storage.Segment` 新增 `TermKeys()`：按 TermID 顺序返回词典 key，供合并遍历 (field, value, posting)

**文件改动：**

- `segmentindex/store.go`: Store / StoreSnapshot / CompactionOptions / 后台 worker
- `segmentindex/merge.go`: mergeDeltas、groupSignature 与不相交合并
- `segmentindex/version.go`: 序列号 tombstoneSet 与回收
- `segmentindex/delta.go`: 不可变 Delta 批次
- `segmentindex/ratelimit.go`: RateLimiter / TokenBucket
- `segmentindex/index.go` / `boolean.go`: groupMeta（shapes/ranges/seqs）、tombstone 过滤、query 副本修复
- `storage/segment.go`: TermKeys()
- 测试：`store_test.go`、`merge_test.go`（快照稳定性、tombstone、合并保序、失败恢复、限速、后台合并）

### Performance

#### segmentindex 查询索引优化

为离线构建、在线查询场景增加不可变查询索引与严格对比 benchmark，构建阶段额外预计算 term/range postings、单文档 group 快速路径和数值 term 的 `int64` 索引；`RetrieveAssignments` 对高选择性精确查询避免逐字段构造 Roaring bitmap，并复用查询 scratch、直接映射结果 DocID，对宽召回查询继续使用 bitmap 回退路径。

**文件改动：**

- `segmentindex/query.go`: dense `(group, doc)` slot 查询索引、exact-term 快速路径、range point/interval 索引
- `benchmark/retrive_test.go`: `github.com/echoface/be_indexer` API 迁移及 KGroups/segmentindex 严格同数据同查询 benchmark
- `segmentindex/query_test.go`: 数值/字符串 term 语义回归测试
- `segmentindex/boolean.go`: `RetrieveAssignmentsInto` 与 tombstone 过滤结果复用
- `segmentindex/query.go`: 查询 scratch 池、短数值 slice 存储与结果缓冲复用

---

## [Unreleased] - 2026-02-10

### Added

#### segmentindex 范围谓词支持

`segmentindex` 现已支持数值范围表达式（`GreaterThan` / `LessThan` / `Between` 及自定义 `ValueOptGT/LT/Between` 表达式），不再将 range 视为不支持的表达式：

1. **新增 `parsePredicateRange`**：将范围表达式解析为半开区间 `[left, right)`，语义与 `holder/rangeholder.ParseRange` 完全一致
   - `GT v` → `[v+1, MaxInt64)`（严格大于）
   - `LT v` → `[MinInt64, v)`（严格小于）
   - `Between l,h` → `[l, h)`（右边界不包含；`l == h` 时覆盖 `l` 自身）
   - 浮点数会被拒绝（不静默截断）；`Between` 额外支持 `[2]int64`、双元素切片和 `"x:y"` 格式
2. **`RetrieveAssignments` 评估范围谓词**：命中 include range 时放行该组 live docs，命中 exclude range 时将其从结果中剔除；查询字段缺失时跳过该字段约束（与既有精确匹配语义一致）
3. **`Index.Retrieve`（planner 精确 term 路径）不评估范围**：文档仅含范围约束时该路径可能漏报（不会误报），完整 DNF 语义请使用 `RetrieveAssignments`（已补充注释说明）

#### Parser Interface Refactoring

重构 Parser 接口设计，引入 `ValueTokenizer` 和 `ValueIDGenerator` 两个核心接口，使代码更加清晰和灵活。

**主要变更：**

1. **新增 `ValueTokenizer` 接口**
   - 用于 `DefaultEntriesHolder`，将值转换为字符串列表
   - 支持双向解析：
     - `TokenizeValue`: 索引阶段，解析布尔表达式的值
     - `TokenizeAssign`: 查询阶段，解析查询参数
   - GeoHashParser、NumberParser 等已实现此接口

2. **`ValueIDGenerator` 接口增强**
   - 添加 `Name()` 方法，用于标识解析器
   - 用于 `roaringidx` 包，将值转换为 uint64 ID
   - 保持向后兼容：`FieldValueParser` 作为别名保留

3. **GeoHashParser 现在同时实现两个接口**
   - `ValueTokenizer`: 供 `DefaultEntriesHolder` 使用，生成 geohash 字符串
   - `ValueIDGenerator`: 供 `roaringidx` 使用，生成 uint64 ID
   - 核心逻辑基于字符串生成，uint64 通过字符串转换获得，符合 geohash 自然流程

**API 变更：**

```go
// 新的接口定义
type ValueTokenizer interface {
    TokenizeValue(v interface{}) ([]string, error)   // 索引阶段
    TokenizeAssign(v interface{}) ([]string, error)  // 查询阶段
}

type ValueIDGenerator interface {
    Name() string
    ParseAssign(v interface{}) ([]uint64, error)
    ParseValue(v interface{}) ([]uint64, error)
}

// 向后兼容的别名
type FieldValueParser = ValueIDGenerator  // Deprecated
```

**使用方式更新：**

```go
// DefaultEntriesHolder: 使用 RegisterFieldTokenizer
holder := be_indexer.NewDefaultEntriesHolder()
holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())
holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))

// roaringidx: 使用 ValueIDGenerator（即 FieldValueParser）
builder.ConfigureField("geo", roaringidx.FieldSetting{
    Container: roaringidx.ContainerNameDefault,
    Parser:    parser.NewGeoHashParser(nil),  // 自动适配 ValueIDGenerator
})
```

**向后兼容性：**

- `FieldValueParser` 作为 `ValueIDGenerator` 的别名保留，现有代码无需修改
- 所有原有 Parser 已实现 `Name()` 方法
- roaringidx 的使用方式保持不变

**文件改动：**
- `parser/types.go`: 新增接口定义和别名
- `parser/geohash_parser.go`: 实现双接口
- `parser/number_parser.go`: 实现 ValueTokenizer 接口
- `parser/str_parser_hash.go`: 添加 Name() 方法
- `parser/range_parser.go`: 添加 Name() 方法

---

## [Unreleased] - 2026-02-08

### Added

#### 增量索引构建 (Incremental Index Building)

新增文档级缓存机制，支持增量索引构建，大幅提升广告检索等场景下的构建性能。

**主要特性：**

1. **Document.Version 字段**
   - 新增 `Version uint64` 字段到 Document 结构体
   - 由业务层提供版本号（时间戳或序列号）
   - Version = 0 表示不使用缓存（向后兼容）

2. **DocLevelCache 接口**
   ```go
   type DocLevelCache interface {
       Get(key DocCacheKey) (*DocCacheEntry, bool)
       Set(key DocCacheKey, entry *DocCacheEntry)
       Clear()
   }
   ```
   - 业务可自定义缓存实现（内存、Redis、文件等）
   - 提供内存缓存示例实现

3. **IndexerBuilder 集成**
   - 新增 `WithDocLevelCache(cache DocLevelCache)` Builder 选项
   - 自动检测缓存命中并恢复
   - 自动保存新编译结果到缓存

4. **Schema Hash 校验**
   - 计算字段配置的哈希值
   - Schema 变化时自动清空缓存
   - 避免配置变更导致的数据不一致

**技术细节：**

- **缓存粒度**：Document 级别，每个 Document 独立缓存
- **缓存内容**：序列化后的 TxData（通过 TxData.Encode() 生成）
- **恢复机制**：通过 holder.DecodeTxData() 解码，保持接口抽象
- **类型安全**：不依赖具体 Holder 类型，支持 Default、AC、Range 等所有 Holder

**性能表现：**

在示例场景中（3 个文档，1 个更新）：
- 全量构建：237µs
- 增量构建：70µs
- **性能提升：3.3x**

对于生产环境（百万级文档，5-10%更新率），预期可实现 **5-10x 性能提升**。

**文件改动：**

- `document.go`: 添加 Version 字段
- `doc_cache.go`: 新增缓存接口和结构定义
- `index_builder.go`: 集成缓存逻辑（restoreFromCache, captureConjResult）
- `example/incremental_indexer/main.go`: 使用示例
- `doc_cache_test.go`: 单元测试

**向后兼容性：**

- Version = 0 时行为不变，完全向后兼容
- 现有代码无需修改即可正常工作
- 可选启用增量构建功能

---

## [2023-03-25]

### Added

- 支持在同一个 Conjunction 中添加同一个 field 的逻辑表达
  - eg: `{field in [1, 2, 3], not-in [2, 3, 4]} and .....`
  - input field:4 => true
  - input field:3 => false（not 有更高逻辑优先级）
  - 本库实现对逻辑 true 更严格
  - 在 roaringidx/be_indexer 两份逻辑实现中保持一致
  - 更多细节见: `./example/repeat_fields_test`

---

## [Earlier Versions]

### Features

- Boolean expression indexing 核心实现
- Roaring bitmap based indexing
- AC 自动机模式匹配容器
- 数值范围容器（支持 >, <, between 等运算符）
- 自定义 Parser 和 Holder
- 支持多值特征查询
- Compacted index 模式（性能提升约 12%）

### Limitations

- 文档 ID 最大值限制：`[-2^43, 2^43]`
- 单个 Conjunction 数量小于 256 个
- 使用前需要为每个字段完成配置

## Migration Guide

### From Older Versions to v2026.02.08

1. **无需改动**：现有代码完全向后兼容
2. **启用增量构建**（可选）：
   ```go
   // 添加缓存
   cache := NewMemoryDocCache()
   builder := be_indexer.NewIndexerBuilder(
       be_indexer.WithDocLevelCache(cache),
   )
   
   // 设置 Version
   doc := be_indexer.NewDocument(id)
   doc.Version = uint64(updateTime.Unix())
   ```

## Contributing

欢迎提交 Issue 和 PR！

## License

MIT License - see [LICENSE](LICENSE) file for details.
