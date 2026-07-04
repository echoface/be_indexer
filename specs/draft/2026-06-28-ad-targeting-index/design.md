# 广告定向索引库 v2 技术设计

## 1. 总体架构

```text
业务 DB 定向数据
  -> 业务侧 Normalize / DNF 化 / Mutation 聚合
  -> builder 构建 full 或 delta segments
  -> manifest 描述完整快照
  -> serving Loader mmap 加载 snapshot
  -> CompositeEngine 同时查询 full/delta 并合并结果
```

分层：

```text
core      : Document / Conjunction / Predicate / ConjID / EntryID / LiveDocs
builder   : Document[] -> Segment，保持 VLDB09 K-Groups 构建
segment   : mmap-friendly Dict / PostingList / Reader / Writer
manifest  : 快照元数据、segment 描述、watermark、checksum、schema hash
engine    : BooleanEngine + CompositeEngine / IndexSnapshot
```

## 2. 64-bit 编码

当前 `core/id_types.go:14` 中的布局继续作为 v2 基线：

```text
ConjID:
bit 63..60  reserved，必须为 0
bit 59..52  K / Size
bit 51..44  ConjIndex
bit 43      Sign
bit 42..0   abs(DocID)

EntryID:
bit 63..4   ConjID payload
bit 3..1    unused
bit 0       include/exclude，0=exclude，1=include
```

排序不变量：

```text
same ConjID:
exclude = ConjID << 4 | 0
include = ConjID << 4 | 1
exclude < include
```

这保证 `engine/searcher.go:88` 的 `retrieveK` 可以在同一 ConjID 对齐时通过最小 EntryID 判断是否 exclude。

## 3. exclude-on-present-value

设计语义：exclude 只有在 query 显式提供该字段并命中排除值时触发。query 缺字段不触发 exclude。

该设计依赖当前行为：

- `engine/searcher.go:187` 遍历 query 中出现的字段。
- `engine/searcher.go:194` 空 value 被跳过。
- `engine/searcher.go:217` 只查 query value 对应的 posting。
- `engine/searcher.go:118` exclude entry 参与 merge 后才短路排除。

纯 exclude conjunction 的 K 为 0，由 wildcard/Z-list 兜底；当前构建逻辑在 `builder/doc_exporter.go:117`。

## 4. manifest + 多 segment

建议目录：

```text
index_root/
  CURRENT
  manifests/manifest-000001.json
  full/full-000001/segment-000000.bei
  full/full-000001/wildcards.bin
  delta/delta-000002/segment-000000.bei
  delta/delta-000002/changed_docs.bin
  delta/delta-000002/deleted_docs.bin
  tmp/building-xxxx/
```

manifest 必须表达：

- `generation`
- `schema_hash`
- `format_version`
- full generation 与 watermark
- delta generation 与 watermark range
- segment 文件列表、大小、checksum、doc_count、doc range
- wildcard sidecar checksum
- changed_docs / deleted_docs sidecar checksum
- builder version

短期设计使用 sidecar `wildcards.bin`，因为现有 `BuildSegmentFromDocs` 返回 wildcard entries，`BooleanEngine` 构造也需要外部传入 wildcard entries：`be_indexer.go:121`、`engine/searcher.go:21`。中长期 Segment v2 应把 Z-list 内置为 section。

## 5. full + delta 查询合并

不能把 full segments 与 delta segments 放进同一个 `BooleanEngine` 再挂全局 `LiveDocs`。原因是当前 `BooleanEngine` 只有一个 `liveDocs` 字段：`engine/searcher.go:17`，会同时过滤 full 与 delta，update/recreate 场景会误杀 delta 新版本。

新增 `CompositeEngine / IndexSnapshot`：

```go
type IndexSnapshot struct {
    Generation uint64
    FullEngine  *BooleanEngine
    DeltaEngine *BooleanEngine
    ChangedDocs DocSet
    DeletedDocs DocSet
}

Result = (FullResult - ChangedDocs) ∪ (DeltaResult - DeletedDocs)
```

语义：

| 场景 | full | delta | changed | deleted | 结果 |
|---|---|---|---|---|---|
| 未变更 | old | none | no | no | full 命中返回 |
| 新增 | none | new | yes | no | delta 命中返回 |
| 更新后命中 | old | new | yes | no | 返回 delta |
| 更新后不命中 | old | new 不命中 | yes | no | 不返回 |
| 删除 | old | none | yes | yes | 不返回 |
| delete 后 recreate | old | latest active | yes | no | 返回 latest active |

## 6. Delta 构建输入约束

delta 构建前必须按 DocID 聚合：

```text
Map[DocID]LatestMutation
只保留 Version 最大的 mutation
```

如果 latest mutation 是 upsert：写入 delta segment，加入 changed_docs，不加入 deleted_docs。
如果 latest mutation 是 delete：不写 posting，加入 changed_docs 与 deleted_docs。

## 7. Compact 策略

Minor compact 触发建议：

- delta segment 数 > 16
- delta docs > full docs 5%
- delta postings bytes > full postings bytes 10%
- changed_docs > full docs 5%
- full+delta p99 > full-only p99 1.2x

Major rebuild 触发建议：

- changed_docs > full docs 20%
- delta postings bytes > full postings bytes 30%
- tombstone 过滤率 > 20%
- 距上次 full build 超过 24h/48h

## 8. 验证门槛

必须覆盖：

1. 64-bit 编码边界：DocID、K、ConjIndex、posting count。
2. exclude missing：字段缺失、空值、命中/未命中排除值。
3. full+delta：update 命中、update 不命中、delete、delete 后 recreate、多次 update、去重、delta 优先。
4. manifest：schema hash mismatch、segment checksum mismatch、缺文件、混 generation、rollback。
5. shadow：真实变更流回放，索引结果与 oracle 100% 一致。

## 9. 构建产物目录生成

下一阶段补齐业务接入最关键的离线构建闭环：业务侧输入 full docs 或 delta mutations，库负责生成 loader 可直接消费的 segment、sidecar 与 manifest descriptor。

### 9.1 API

```go
type BuildDirectoryOptions struct {
    MaxDocsPerSegment int
    BuilderVersion    string
}

type FullBuildRequest struct {
    Root              string
    Generation        uint64
    SnapshotWatermark uint64
    Fields            map[core.BEField]*core.FieldMeta
    Documents         []*core.Document
    Options           BuildDirectoryOptions
}

func BuildFullIndexDir(req FullBuildRequest) (manifest.FullIndexDescriptor, error)

type DeltaBuildRequest struct {
    Root                   string
    Generation             uint64
    FromWatermarkExclusive uint64
    ToWatermarkInclusive   uint64
    Fields                 map[core.BEField]*core.FieldMeta
    Mutations              []builder.Mutation
    Options                BuildDirectoryOptions
}

func BuildDeltaIndexDir(req DeltaBuildRequest) (manifest.DeltaIndexDescriptor, error)
```

### 9.2 输出目录

```text
index_root/
  full/full-000001/
    segment-000000.bei
    wildcards.bin
  delta/delta-000002/
    segment-000000.bei        # delete-only delta 可不存在
    wildcards.bin
    changed_docs.bin
    deleted_docs.bin
```

构建过程必须先写入 `tmp/building-*`，所有文件、checksum、descriptor 生成成功后，再 rename 到最终 generation 目录。manifest 与 CURRENT 切换继续由 `manifest.PublishManifest` 完成，保证 serving 只看到完整快照。

### 9.3 关键语义

- 不改 `retrieveK`、K-Groups、ConjID/EntryID 编码；目录构建层只编排现有 `BuildSegmentFromDocs`。
- segment descriptor 的 `Size/Checksum` 必须基于最终写入 bytes。
- wildcard sidecar 即使为空也写入，避免 manifest 语义歧义。
- delta 必须先调用 `BuildDeltaPlan`，只保留同 DocID 最大 version：upsert 写入 delta segment，delete 只写 changed/deleted sidecar。
- delete-only delta 允许 `Segments` 为空，但必须有 changed_docs sidecar；loader 通过 `ChangedDocs` 剔除 full 旧版本。
- 多 delta 加载时 `DeletedDocs` 必须表示最终删除态：按 watermark 顺序处理每个 delta，先用该 delta 的 changed_docs 清除旧 deleted 标记，再加入该 delta 的 deleted_docs，保证 delete 后 recreate/upsert 不会被历史 tombstone 误杀。
- manifest 路径必须拒绝绝对路径、`..` 逃逸和带目录分隔符的 sidecar/segment 文件名。

## 10. 真正流式 full 构建

百万级广告定向文档不能要求业务一次性组装 `[]*Document`，也不能让单个 segment 在内存中持有全部 posting。流式 full 构建采用两层 bounded-memory 设计：

```text
DocumentIterator
  -> export single doc to posting records
  -> ExternalBuilder in-memory buffer reaches MaxPostingsInMemory 后 spill sorted run
  -> segment 完成时 k-way merge runs
  -> 直接写 segment 文件、再流式计算 checksum
```

### 10.1 API

```go
type DocumentIterator interface {
    Next() (*core.Document, bool, error)
}

type FullStreamBuildRequest struct {
    Root              string
    Generation        uint64
    SnapshotWatermark uint64
    Fields            map[core.BEField]*core.FieldMeta
    Documents         DocumentIterator
    Options           BuildDirectoryOptions
}

type BuildDirectoryOptions struct {
    MaxDocsPerSegment     int
    MaxPostingsInMemory   int
    MaxWildcardEntriesInMemory int
    BuilderVersion        string
}

func BuildFullIndexDirFromIterator(req FullStreamBuildRequest) (manifest.FullIndexDescriptor, error)
```

兼容入口 `BuildFullIndexDir` 会包装 `[]*Document` 为 iterator，但新业务大规模 full build 应优先使用 iterator API。

### 10.2 内存边界

- 不持有全量 documents；一次只从 iterator 取一个 document。
- 每个 segment 的 posting 内存由 `MaxPostingsInMemory` 限制，达到阈值写入一个排序 run 文件。
- segment 结束后通过 k-way merge 合并 runs；内存主要为：`O(MaxPostingsInMemory + run_count + 当前 term posting list + 当前 group dict)`。
- segment bytes 不再通过 `bytes.Buffer` 整体驻留内存，而是写临时文件，完成后对文件流式计算 sha256。
- wildcard/Z-list sidecar 也采用外排 run：`MaxWildcardEntriesInMemory` 控制内存，最终流式 merge 写 sidecar 并计算 sha256，避免纯 K=0 语料导致全量 wildcard entries 常驻内存。

### 10.3 格式兼容

ExternalBuilder 仍输出现有 segment v1 格式：magic、posting block、dict block、AC block、metadata、footer 均保持不变；loader 和 serving 检索路径无需修改。

### 10.4 正确性约束

- run 排序 key 必须为 `(K, field, term, EntryID)`，保证 merge 后每个 term 的 posting list 按 EntryID 有序。
- wildcard/Z-list 仍由 doc exporter 返回并写 sidecar。
- `MaxDocsPerSegment` 仍控制 segment doc_count 上限；`MaxPostingsInMemory` 控制 posting spill 上限。
- run merge 必须限制同时打开的 run reader 数量，超过阈值时多轮 compaction，避免百万级构建触发 FD 上限。
- tmp run 文件必须在成功或失败后清理，不能污染最终发布目录。

## 11. Compact 策略自动化与多 Delta 正确性

compact 自动化先提供 manifest 级静态统计与决策 API，不直接执行 compact，不从 segment 反推 document，避免破坏 VLDB09 serving 核心。

### 11.1 manifest 统计字段

delta descriptor 增加：

```go
ChangedDocCount uint64 `json:"changed_doc_count,omitempty"`
DeletedDocCount uint64 `json:"deleted_doc_count,omitempty"`
```

`BuildDeltaIndexDir` 在构建 changed/deleted sidecar 时同步填充 count。老 manifest 该字段缺失时仍可加载，但 compact 决策会标记 `HasExactChangeCounts=false`，不基于 changed/tombstone 比例触发决策。

### 11.2 compact 包

新增 `compact` 包：

```go
type Decision string // none/minor/major
type Stats struct { ... }
type Policy struct { ... }
type RuntimeObservation struct { FullOnlyP99, FullDeltaP99, FullAge time.Duration }
func CollectStats(m manifest.Manifest) Stats
func Decide(m manifest.Manifest, opts Options) Recommendation
func DecideStats(stats Stats, opts Options) Recommendation
```

默认阈值：

- minor：delta segments > 16、delta docs/full docs > 5%、delta bytes/full bytes > 10%、changed/full > 5%、full+delta p99/full-only p99 > 1.2。
- major：changed/full > 20%、delta bytes/full bytes > 30%、deleted/changed > 20%、full age > 48h。
- major 优先于 minor；仅当 value > threshold 时触发，等于阈值不触发。

### 11.3 多 delta update/update 正确性

多个 delta 不能简单合成一个 `BooleanEngine`。否则：

```text
delta-2: doc7 -> city=bj
delta-3: doc7 -> city=sh
query city=bj 不应返回 doc7，但旧 delta 仍会命中。
```

loader 必须为每个 delta 构建独立 engine，并从后向前累计 later changed_docs，把这些 DocID 写入较老 delta engine 的 LiveDocs 删除集。最终查询对每个 delta engine 分别检索，再用全局 final DeletedDocs 过滤 delete 最终态。这样能保证后续 update 覆盖旧 delta 版本。

### 11.4 full/delta benchmark

新增 `BenchmarkCompositeEngineFullVsFullDelta`，同一批 synthetic full documents 下分别构造：

- `full_only`：仅 FullEngine 的 CompositeEngine baseline。
- `full_delta`：FullEngine + 单独 DeltaEngine + ChangedDocs/DeletedDocs merge 层。

benchmark 使用相同 query，输出 ns/op 与 alloc/op，用于量化 full+delta 合并层相对 full-only 的额外开销，并为 `RuntimeObservation{FullOnlyP99, FullDeltaP99}` 阈值输入提供离线校准依据。

## 12. Segment v2：内置 Z-list、schema hash、block checksum

Segment v2 在不改 VLDB09 检索算法、不改变 posting/dict/AC block 二进制结构的前提下，只扩展物理文件 envelope 与 metadata：

- magic 固定为 `BEIDX\x00\x00\x02`。
- metadata `version=2`，新增 `schema_hash`、`wildcards_block`、`block_checksums`。
- Z-list / wildcard entries 写入 segment 内部 `__wildcards` block，编码沿用 entries 格式，loader 始终从 segment 读取内置 wildcards。
- 每个 data block 写入 sha256 checksum 到 metadata，reader 加载 v2 时先校验 block 边界与 checksum，再初始化 dict/postings/AC reader。
- 旧版历史格式不再兼容；reader 仅接受 v2 magic/version，并强制要求内置 wildcards 与 block checksums。

### 12.1 API 与兼容策略

- `segment.NewBuilderWithOptions` / `segment.NewExternalBuilder` 仅写 v2。
- `builder.BuildSegmentFromDocsWithOptions` 与根包 `BuildSegmentWithOptions` 暴露单 segment v2 构建入口。
- `BuildDirectoryOptions{SegmentSchemaHash}` 允许目录构建把 manifest schema hash 嵌入每个 v2 segment。
- manifest 层不再保留 wildcard sidecar 字段，Z-list 是 segment v2 的必选内置 block。

### 12.2 正确性约束

- v2 reader 必须拒绝 magic/version 不匹配、未知版本、越界 block、checksum mismatch。
- embedded Z-list 必须排序后写入，保证与 sidecar 结果一致。
- checksum 覆盖 postings/dict/AC/wildcards block，不覆盖 metadata/footer；metadata 仍由 manifest 文件级 checksum 保护。
- 外排流式构建同样写 v2 metadata 与 checksums，不能退化成 v1。

### 12.3 manifest / segment format 一致性保护

manifest 的 `format_version` 必须与物理 segment 版本一致，避免历史格式与现行物理格式混用：

- manifest 只接受 `segment-v2`。
- loader 加载每个 segment 后都必须校验 `reader.Version() == SegmentVersionV2`。
- v2 segment 必须校验内嵌 schema hash 与 manifest schema hash 一致。
- 根包仅导出 `FormatVersionSegmentV2`，避免业务侧硬编码字符串。
