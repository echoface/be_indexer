# be_indexer 使用指南与最佳实践

本文档面向**使用方（业务接入方）**，回答两个问题：怎么用对（语义正确）、怎么用稳（生产可用）。内部实现细节请参考 [ARCHITECTURE.md](../architecture/overview.md)，最小可运行代码请参考 [QUICK_START.md](quick-start.md) 与 [EXAMPLES.md](examples.md)。

## 目录

1. [选型](#1-选型)
2. [规则建模](#2-规则建模)
3. [Schema 与容器选型](#3-schema-与容器选型)
4. [全量构建](#4-全量构建)
5. [增量构建](#5-增量构建)
6. [快照发布](#6-快照发布)
7. [加载与热更新](#7-加载与热更新)
8. [在线查询](#8-在线查询)
9. [容量与性能](#9-容量与性能)
10. [索引合并与全量发布评估](#10-索引合并与全量发布评估)
11. [可观测性与验证](#11-可观测性与验证)
12. [反模式](#12-反模式)

---

## 1. 选型

`be_indexer` 是**离线和在线分离**的库：离线把布尔规则编译成不可变的 Segment 文件，在线只读查询。先确认你的场景属于哪一类。

| 场景 | 推荐路径 | 关键 API |
|:-----|:---------|:---------|
| 规则量小、可接受停机重建、单进程嵌入 | 单段/多段直连 | `BuildSegment` / `BuildSegments` → `NewSegmentReader` / `OpenSegmentFile` → `NewEngine` |
| 生产在线服务、需要不停机更新、多版本规则 | Full + Delta + Manifest + Holder | `NewFullIndexBuilder` / `NewDeltaIndexBuilder` → `NewSnapshotManifest` → `PublishManifest` → `NewIndexHolder` |

只要满足下面任意一条，就应该走第二条路径：

- 每天有规则增删改，且不能接受“重建期间不可用”；
- 规则集大到无法整体物化在内存里（需要流式外部排序）；
- 需要回滚：保留多代 manifest，`CURRENT` 指回旧代即完成回滚。

生产环境请只通过 `loader`（`OpenIndex` / `NewIndexHolder`）加载：它会校验 manifest、schema hash、文件大小与整文件 SHA-256，再建立 mmap。直接调用 `segment` 属于高级用法。

---

## 2. 规则建模

这是最容易出线上事故的部分，务必先理解语义再写规则。

### 2.1 DNF 语义

- 一个 `Document` = 一条业务规则，内部多个 `Conjunction` 之间是 **OR**。
- 一个 `Conjunction` 内的多个字段之间是 **AND**。
- **K = 该 Conjunction 中 Include 字段的个数。** 命中要求这 K 个字段在本次 `Assignments` 里全部被满足。

```go
// 规则：18 <= age <= 25 AND city = "beijing"
doc := be_indexer.NewDocument(1).AddConjunction(
    be_indexer.NewConjunction().Between("age", 18, 25).
        In("city", be_indexer.NewStrValues("beijing")),
)
// K = 2：查询必须同时提供 age 和 city，否则该 Conjunction 不可能命中
```

**关键结论：查询缺少某个 Include 字段时，需要该字段的 Conjunction 一定不命中。** 这不是 bug，而是 K-Groups 的设计。业务上要保证 assignment 字段齐全，或把“字段缺失”显式建模成另一个 Conjunction。

### 2.2 同一字段在一个 Conjunction 内最多一个 Include

构建期会直接拒绝（`builder/normalize.go`），因为同字段多个 Include 会让“AND 语义”与倒排 OR 归并产生歧义。

```go
// 错误：同一字段两个 Include
conj.In("city", be_indexer.NewStrValues("bj")).
     In("city", be_indexer.NewStrValues("sh"))

// 正确：多个等值放进一个 Constraint（取值之间是 OR）
conj.In("city", be_indexer.NewStrValues("bj", "sh"))

// 错误：同字段区间叠加
conj.GreaterThan("age", 18).LessThan("age", 35)

// 正确：合并为一个闭区间
conj.Between("age", 19, 34) // 整数上的 age > 18 AND age < 35
```

同一字段可以有多个 Exclude（任意命中即 veto）。

### 2.3 Exclude 的语义与 Z-Entry

- `Exclude`（`NotIn`）**只在 assignment 显式命中被排除值时否决**。查询没有提供该字段时，没有对应游标，Exclude 不生效——它不能表达“字段缺失即不匹配”。
- **K=0 的 Conjunction 总会产生 wildcard 候选，但命中的 Exclude 仍会否决它。** 空 `NewConjunction()` 才是无条件规则；只带 Exclude 的 Conjunction 也是 K=0。

```go
// 全局兜底规则：除 city 命中 "rural" 外一律命中
global := be_indexer.NewDocument(2).AddConjunction(
    be_indexer.NewConjunction().NotIn("city", be_indexer.NewStrValues("rural")),
)

// 无条件命中的默认规则
fallback := be_indexer.NewDocument(3).AddConjunction(be_indexer.NewConjunction())
```

### 2.4 硬限制

| 项 | 限制 | 说明 |
|:---|:-----|:-----|
| `DocID` | `[-(2^43-1), 2^43-1]` | int64 编码进 64 位 ID，不可超范围 |
| 单文档 Conjunction 数 | < 256 | 超过构建报错 |
| K（单 Conjunction Include 数） | < 256 | 超过构建报错 |
| DocID 唯一性 | 全量构建内必须唯一 | 重复 DocID 会让不同文档的谓词合并成幻觉 Conjunction |

### 2.5 DocID 与 Version 的业务映射

- `DocID` 是规则的主键，由业务 ID 稳定映射得到，**更新用同 DocID + 新 Version，不要换新 DocID**；删除用 Delete tombstone。
- `Mutation.Version` 必须在同一 DocID 上单调递增，它是“最后写入获胜”的唯一依据；同 Version 时 Delete 优先。

---

## 3. Schema 与容器选型

`FieldOption` 是两个**正交轴**：

- `IndexType`：用哪种倒排容器存 posting（决定“term 怎么找到 posting list”）。
- `Encoder`：字段值如何被 tokenize 成 term（决定“业务值变成什么 key”）。

| 业务需求 | `IndexType` | `Encoder` |
|:---------|:------------|:----------|
| 精确等值 / IN / NOT IN，词表 ≤ ~1M | `default`（或空） | `default`（字符串）/ `number`（整型） |
| 超大高熵 key（设备号、UUID、hash），从不需要枚举 | `mph_dict` | `default` / `number` |
| 超大且带前后缀结构的 key（URL、路径） | `fst_dict` | `default` |
| 在一段文本里筛多个关键词 | `ac_matcher` | `ac_matcher` |
| 数值区间（`>`、`<`、`Between`） | `ext_range` | `ext_range` |
| 经纬度半径覆盖 | `default` | `proximitygeo` |

选择要点：

- 没有明确理由时用 `default`：无外部依赖、可枚举、缓存友好。
- `mph_dict` / `fst_dict` 依赖包级 `init()` 注册，使用时需要 side-effect import 对应容器包，否则构造 engine 时会报“unknown index kind”。
- **Schema 是索引身份的一部分（`schemaHash`）。** 改字段名、`IndexType`、`Encoder` 都必须重新全量构建；delta 只能追加在同一 schema 下，否则 `OpenIndex` 会以 schema hash mismatch 拒绝加载。

```go
fields := be_indexer.Schema{
    "city":    {IndexType: be_indexer.IndexNameDefault, Encoder: "default"},
    "age":     {IndexType: be_indexer.IndexNameExtendRange, Encoder: be_indexer.IndexNameExtendRange},
    "keyword": {IndexType: be_indexer.IndexNameACMatcher, Encoder: be_indexer.IndexNameACMatcher},
}
```

---

## 4. 全量构建

全量构建负责“当前完整规则集”的一个不可变代（generation）。它采用流式输入与外部排序，降低语料整体物化的成本；内存并非严格有界，仍取决于热点 key、DocID 去重集合、最终字典以及 range 构建的区间数据。

```go
import "github.com/echoface/be_indexer/builder"

b, err := be_indexer.NewFullIndexBuilder(be_indexer.FullIndexBuildOptions{
    Root:              "/data/index",
    Generation:        20240701,
    SnapshotWatermark: 100,          // full 包含的数据截止位置，也是累计 delta 的起点
    Fields:            fields,
    Options: be_indexer.BuildDirectoryOptions{
        MaxDocsPerSegment:          5_000_000, // 建议单段 ≤ 500 万文档
        MaxDistinctKeysInMemory:    2_000_000, // 每字段 collector 的 distinct key 溢写阈值
        MaxWildcardEntriesInMemory: 1_000_000, // K=0 条目溢写阈值
    },
    FailMode: builder.FailFast,       // 默认：任何文档级错误即失败
})
if err != nil {
    return err // 目标代已存在 (builder.ErrGenerationExists) 或配置错误
}
defer b.Close() // Build 前 abort+清理 tmp，Build 成功后是 no-op

for _, doc := range docs {
    if err := b.AddDocument(doc); err != nil {
        // FailFast 下 builder 已被 poison，后续 Add/Build 都会被拒绝
        return err
    }
}
fullDesc, err := b.Build() // 成功边界 = tmp 目录原子 rename 到最终代目录
```

实践建议：

- **`FailMode` 默认用 `builder.FailFast`**：少一条规则就意味着线上结果错误，宁可不发布。只有在明确接受“跳过坏规则”时才用 `builder.FailSkip`，并通过 `OnSkip func(docID, err)` 落审计日志。
- 文档级错误（校验/编码/重复 DocID）可被 `builder.FailSkip` 跳过；**builder 级错误（磁盘、溢写、rename）永远 fail fast**，无法跳过。
- `MaxDocsPerSegment` 越小，构建峰值内存越低、mmap 粒度越细，但段数增多会增加串行游标归并开销；500 万仅是起始调参建议，应按实际数据验证。
- `MaxDistinctKeysInMemory` 限制的是 **distinct key 数**，不是 posting 数；热点 key 仍会占内存。上线前用真实 key 分布压测峰值。
- `IgnoreUnindexedFields` 默认 false：规则里出现 schema 外字段直接失败。这是安全默认值，不要为了“能跑”轻易打开。
- 同一 `Root` 下同名代目录不可覆盖（`builder.ErrGenerationExists`），世代不可变；用新的 `Generation`。

---

## 5. 增量构建

Delta 表示一个时间窗口内的变更集合，最终查询语义是：

```text
Result = (FullResult - ChangedDocs) ∪ (DeltaResults - DeletedDocs)
```

**业务推荐一个 full + 一个累计 delta**，两者均可包含多个 segment。假设 full 包含截至位置 100 的数据，第一轮 delta 覆盖 `(100, 110]`；下一轮应构建覆盖 `(100, 120]` 的累计 delta 并替换上一轮。只用 `(110, 120]` 替换旧 delta 会丢失前一轮变更，也不满足 Manifest 的连续性校验。业务需保留 full 之后的完整变更，或维护每个 DocID 的最新变更状态（包括删除），作为累计 delta 的构建输入。

```go
import "github.com/echoface/be_indexer/builder"

d, err := be_indexer.NewDeltaIndexBuilder(be_indexer.DeltaIndexBuildOptions{
    Root:                   "/data/index",
    Generation:             202407010001,
    FromWatermarkExclusive: 100,
    ToWatermarkInclusive:   110,
    Fields:                 fields,
    MaxMutations:           1_000_000, // 防止把批量导入误当增量
    FailMode:               builder.FailFast,
})
if err != nil { return err }
defer d.Close()

d.AddMutation(be_indexer.Mutation{
    DocID: 7, Version: 3, Op: builder.MutationUpsert, Document: doc,
})
d.AddMutation(be_indexer.Mutation{
    DocID: 8, Version: 4, Op: builder.MutationDelete,
})
deltaDesc, err := d.Build() // 空 delta 合法，会产出空的 changed/deleted sidecar
```

实践建议：

- **Delta 在内存中累积到 `Build` 才按 DocID 选取最高版本**。设置 `MaxMutations` 上限，并通过适当频率的全量重建限制累计变更规模；大批量改动应走全量重建。
- `Version` 单调递增；`upsert` 覆盖旧版本，`delete` 生成 tombstone，`delete` 后再 `upsert` 更高版本等于“重建”，无需特殊处理。
- 底层支持一个 Manifest 引用多个连续 delta，loader 用后续 delta 的 `changed_docs` 屏蔽旧版本；业务推荐在发布前整理为一个累计 delta，控制查询和版本管理成本。
- Delta 与 Full 必须使用同一份 `Fields`。

---

## 6. 快照发布

发布是把 full 和 delta 的描述符组合成一个不可变快照，并原子更新 `CURRENT`。推荐每个快照引用一个 full 和一个累计 delta；尚无增量时可以只引用 full。

```go
m, err := be_indexer.NewSnapshotManifest(be_indexer.SnapshotManifestRequest{
    IndexName:  "targeting",
    Generation: 202407010001,
    Fields:     fields,
    Full:       fullDesc,
    Deltas:     []be_indexer.DeltaIndexDescriptor{deltaDesc},
})
if err != nil { return err } // 校验 watermark 连续性、路径安全、描述符完整性

// 写入 manifests/<name> 并把 CURRENT 原子切换到它
err = be_indexer.PublishManifest("/data/index", "manifest-202407010001.json", m)
```

必须遵守的约束：

- **watermark 必须连续**：`Full.SnapshotWatermark → delta[0].From → delta[0].To → delta[1].From → …`，`Manifest.Validate` 会强制校验，不允许断档。
- **同名 manifest 不可覆盖**（返回 `ErrManifestExists`）。每代用新文件名；`CURRENT` 通过临时文件 + rename 原子切换。
- `NewSnapshotManifest` 会补齐默认 `FormatVersion` / `PostingEncoding` 并做整体校验，不要手写 `Manifest` 结构体。
- 发布链路（上传、分发、回滚选择）属于业务基础设施；库只产出可校验的描述符，不做复制。
- 回滚 = 把 `CURRENT` 指回旧 manifest（`PublishCurrent`），因为代目录和 manifest 都不可变。

---

## 7. 加载与热更新

### 7.1 加载模式

```go
opts := be_indexer.LoaderOptions{SegmentLoad: be_indexer.SegmentLoadMmapVerify}
```

| 模式 | 行为 | 适用 |
|:-----|:-----|:-----|
| `SegmentLoadHeapVerify` | 读入堆 + 校验尺寸与整文件 SHA-256 | 单测、小规模验证 |
| `SegmentLoadMmapVerify` | 校验尺寸与整文件 SHA-256 后 mmap | **生产默认** |
| `SegmentLoadMmapTrustPublished` | 跳过整文件哈希，仍校验尺寸、段结构、版本、schema hash、block 边界与 checksum 元数据 | 仅当发布链已做可信校验、冷启动速度优先 |

### 7.2 常驻 Holder 与热更新

```go
holder, err := be_indexer.NewIndexHolder("/data/index", fields,
    be_indexer.LoaderOptions{SegmentLoad: be_indexer.SegmentLoadMmapVerify})
if err != nil { return err }
defer holder.Close()

// 收到 manifest 更新通知后
if err := holder.Reload(); err != nil {
    // 加载失败保持旧快照继续服务，记录告警即可
}

res, err := holder.Retrieve(be_indexer.Assignments{"age": []int{20}})
```

- 每进程持有一个 `Holder` 单例即可；引擎与快照不可变，`Retrieve` 并发安全。
- `Reload` 先完整加载并校验新快照，成功后才发布；失败时旧快照继续服务。`CURRENT` 未变化时 `Reload` 是 no-op，不会重复 mmap。
- 旧快照按引用计数回收：`Reload` 后仍在执行的查询会继续持有旧 mmap，最后一个查询结束后才 unmap。
- 不要跨 `Reload` 持有 `UnsafeCurrent()` 返回的引擎（仅供诊断）；查询请走 `Retrieve` / `RetrieveWithCollector`。
- 进程退出调用 `holder.Close()`。

---

## 8. 在线查询

### 8.1 Assignments 的形态

`Assignments = map[FieldName]Values`，`Values` 是 `any`，取值需与字段 `Encoder` 匹配：

```go
queries := be_indexer.Assignments{
    "age":     []int{20},                 // number encoder 接受 int/int64/[]int/[]int64...
    "city":    []string{"beijing"},       // default 接受 string/[]string
    "keyword": "I love go",               // ac_matcher 接受一段文本
}
res, err := engine.Retrieve(queries)
```

也可以用 `NewIntValues` / `NewStrValues` 等显式构造。同一个字段沿用同一 Encoder 语义，不要混用类型。

### 8.2 未知字段与错误处理

- **不在 schema 里的查询字段会被静默忽略**，这不会报错。业务侧应在查询入口按 schema 白名单过滤，避免上游字段名拼错导致“静默漏召回”。
- 默认 lenient：已知字段的编码失败（如把 `"abc"` 传给 number encoder）会被跳过（可能漏掉 Include，也可能绕过 Exclude）。建议通过同时实现 `RetrieveObserver` 和 `FieldErrorObserver` 的 observer 上报 `OnFieldError`，把它和真正的“无匹配”区分开。
- 数据质量要求高时用 `WithStrictQuery()`：任一字段编码/容器查找失败即整体返回错误，而不是静默不命中。

```go
res, err := engine.Retrieve(queries, be_indexer.WithStrictQuery())
```

### 8.3 结果与内存复用

- `Retrieve` 返回 `*BitmapDocSet`，是拷贝，可安全持有；用 `Cardinality()` / `Contains()` / `ForEach()` 访问。
- 高频路径用 `RetrieveWithCollector` 复用调用方拥有的 collector，减少分配；每个并发查询独占一个实例：

```go
collector := be_indexer.NewDocIDCollector() // 每个 worker 创建一次
collector.Reset() // 每次查询前重置
if err := engine.RetrieveWithCollector(queries, collector); err != nil { return err }
collector.Bitmap().ForEach(func(id be_indexer.DocID) { /* ... */ })
```

- `Bitmap()` 是借用视图，与 collector 共享存储，Reset/后续查询会修改它。需要长期持有时调用 `collector.Bitmap().Clone()`。
- 普通自定义 collector 每次查询收到唯一、可见的 DocID，顺序未指定。`CollectStop` 会停止整个 full/delta 查询；返回 error 时 collector 可能含部分结果。
- 只有能自行处理重复且不额外消耗配额的 collector 才应返回 `AcceptsDuplicates() == true`。该能力与 `CompetitiveCollector` 分数剪枝独立。

### 8.4 并发

引擎与快照不可变，`Retrieve` 可被高并发调用；`Holder` 负责 reload 期间的引用计数。**Builder 不是并发安全的**：`AddDocument` / `AddMutation` 必须串行调用。

---

## 9. 容量与性能

| 维度 | 建议 |
|:-----|:-----|
| 单段文档数 | ≤ 500 万（`MaxDocsPerSegment`） |
| 构建内存 | 用 `MaxDistinctKeysInMemory` + `MaxWildcardEntriesInMemory` 控制溢写；注意阈值按 distinct key 计，热点 key 仍占内存 |
| 查询路径分配 | 复用 `ResultCollector`；避免在热路径拼装额外结构 |
| 冷启动延迟 | mmap 首次查询触发缺页，发布后建议预热查询，避免尾部延迟抖动 |
| 并发度 | 单次查询串行组合各段游标，full/delta 也依次执行；通过并发查询利用多核，增加段数不会自动并行 |

关于 mmap 的零拷贝边界：在 little-endian 主机且输入对齐时，posting 数据与部分索引块借用 mmap；big-endian 或非对齐输入会显式解码，但 FlatDict 的词条元数据、Range/AC 的部分结构会在打开时解码到堆内存。也就是说“posting zero-copy”成立，“整个 SegmentReader zero-copy”不成立。真实内存占用请以压测为准，不要只按文件大小估算。

---

## 10. 索引合并与全量发布评估

根包的 [`api_maintenance.go`](../../api_maintenance.go) **只给业务提供评估参考**：根据索引规模、变更比例和可选运行指标，判断是否值得合并 delta，或重新构建并发布 full。调用后只得到建议和触发原因；数据读取、索引构建、发布和 Reload 由业务安排。

推荐在业务构建任务中按需调用；已有固定全量发布周期的业务也可以不用此功能。下例读取当前 Manifest 并返回建议，由调用方决定是否安排任务：

```go
package example

import (
    be "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/loader"
)

func EvaluateIndexMaintenance(root string) (be.IndexMaintenanceRecommendation, error) {
    m, err := loader.ReadCurrentManifest(root)
    if err != nil {
        return be.IndexMaintenanceRecommendation{}, err
    }
    return be.EvaluateIndexMaintenance(m, be.IndexMaintenanceOptions{}), nil
}
```

| 返回建议 | 含义 | 业务可安排的操作 |
| :--- | :--- | :--- |
| `none` | 没有触发配置的阈值 | 保持当前发布计划 |
| `minor` | delta 的段数、占比或查询延迟增幅超过阈值 | 评估是否从变更数据重新构建一个累计 delta，替换旧 delta |
| `major` | 变更占比、删除占比、delta 字节占比或距 full 构建完成的时长超过阈值 | 评估是否从权威数据源重新构建并发布 full |

例如，一个 delta 内有 17 个 segment，且没有触发 major 条件时，默认返回 `minor`，原因是段数大于 16。**一个 delta 不等于一个 segment**，所以推荐一个 full + 一个 delta 与段数评估并不矛盾。阈值只供参考：合并不会必然减少不同 Document 的数量，也不保证查询延迟下降，应结合实际构建成本和查询性能决定。

业务根据所选任务准备构建输入：

- **合并/重建 delta**：取得当前 full 数据截止位置之后的累计 Mutation，交给 `NewDeltaIndexBuilder`；构建器按 DocID 选最高版本，并保留最终删除状态以屏蔽 full 中的旧 Document。构建器接收 Mutation，不直接合并旧 segment 文件。
- **全量重建**：从权威数据源取得某一确定数据截止位置的全部有效 Document，交给 `NewFullIndexBuilder`；构建期间产生的新变更，应作为新 full 截止位置之后的累计 delta 接续发布。

构建完成后，用 `NewSnapshotManifest` 组合对应的 full/delta 描述符，再 `PublishManifest` 原子更新 `CURRENT`，由服务调用 `holder.Reload()` 加载。full/delta 目录和 Manifest 都使用新版本，不原地修改线上文件。

`IndexMaintenanceRuntimeObservation.TimeSinceFullBuild` 的精确定义是 **评估时间减去当前 full 构建完成时间**，例如业务记录 `fullBuiltAt`，评估时传 `time.Since(fullBuiltAt)`。它不是进程运行时长，也不是最近一次 delta 发布后的时长；库不会从 Manifest 自动推算。P99 同样由业务采集并传入。默认 `IndexMaintenanceOptions{}` 不提供这些运行指标，因此示例只根据 Manifest 统计评估。默认阈值、计数和比例的边界见 [API 参考](api-reference.md#索引合并与全量发布评估)。

---

## 11. 可观测性与验证

### 11.1 查询指标

```go
res, err := engine.Retrieve(queries, be_indexer.WithObserver(myObserver))
```

`RetrieveObserver` 提供 `OnRetrieveStart` / `OnRetrieveEnd` / `OnConjunctionMatch` / `OnExcludeSkip` / `OnCursorInit`；实现 `core.FieldErrorObserver` 还能收到 `OnFieldError`。建议至少采集：命中数、耗时、被跳过的字段错误数、exclude skip 次数。

### 11.2 发布前校验

- 构建描述符是自描述的：可用 `manifest.SHA256File` 逐段核对 `desc.Size` / `desc.Checksum`（见 `examples/push_builder`）。
- 冷启动加载会校验 schema hash；schema 变更后旧 delta 一定加载失败，这是有意设计。

### 11.3 上线验证

- 核心检索相关改动必须做 shadow testing：用 `BooleanEngine` 的检索结果与原始规则逐条求值结果对比随机查询，保证 100% 一致。
- 推荐先离线跑“同数据双写对比”（新旧实现各查一批随机 assignment，比对 DocID 集合），再灰度切换。

---

## 12. 反模式

- 用一个进程级长期持有的 `BooleanEngine` 而不走 `Holder`，导致无法热更新、旧 mmap 无法及时回收。
- 用大 delta 代替全量重建：delta 是全内存累积的，且 `MaxMutations` 本就是为了拦住这种用法。
- 用“换新 DocID”表达更新，或 `Version` 不单调；结果是旧规则残留或新规则被遮蔽。
- 同名 manifest 反复覆盖，或 watermark 断档，导致 `PublishManifest` / 加载失败。
- 同一字段在一个 Conjunction 里写两个 Include；或指望 Exclude 处理“字段缺失”。
- 生产环境为了冷启动速度使用 `SegmentLoadMmapTrustPublished`，但发布链并没有可信校验。
- 查询字段名拼错后依赖报错——未知字段会被静默忽略，必须在入口做 schema 白名单校验。
- 在热路径重复分配 collector 或拼接大量临时结构，抵消零拷贝收益。
- schema 改了却沿用旧 delta，或试图复用旧 full。

---

## 参考

- [QUICK_START.md](quick-start.md) — 端到端最小示例
- [EXAMPLES.md](examples.md) — 常见场景配方
- [ARCHITECTURE.md](../architecture/overview.md) — 分层设计、ID 编码、检索算法
- [API_REFERENCE.md](api-reference.md) — 包级 API 参考
- [indexes/README.md](../architecture/indexes/README.md) — 各索引容器选型与复杂度
- [../../examples/push_builder/main.go](../../examples/push_builder/main.go) — full + delta + publish + load 的可运行端到端示例
