# 快速开始

## 1. 定义 Schema

Define which fields to index and how values are tokenized:

```go
import "github.com/echoface/be_indexer"

fields := be_indexer.Schema{
    "age":  {IndexType: be_indexer.IndexNameExtendRange, Encoder: be_indexer.IndexNameExtendRange},
    "city": {IndexType: be_indexer.IndexNameDefault, Encoder: "default"},
    "tag":  {IndexType: be_indexer.IndexNameACMatcher, Encoder: be_indexer.IndexNameACMatcher},
}
```

## 2. 构造 Document

Documents encode ad targeting rules in DNF (OR of ANDs):

```go
// Doc 1: 18 <= age <= 25 AND city = "beijing"
doc1 := be_indexer.NewDocument(1)
c1 := be_indexer.NewConjunction()
c1.Between("age", 18, 25)
c1.In("city", be_indexer.NewStrValues("beijing"))
doc1.AddConjunction(c1)

// Doc 2: city NOT IN "rural"  (K=0 wildcard)
doc2 := be_indexer.NewDocument(2)
c2 := be_indexer.NewConjunction()
c2.NotIn("city", be_indexer.NewStrValues("rural"))
doc2.AddConjunction(c2)

// Doc 3: age > 30 AND tag matches either "vip" or "premium"
doc3 := be_indexer.NewDocument(3)
c3 := be_indexer.NewConjunction()
c3.GreaterThan("age", 30)
c3.In("tag", be_indexer.NewStrValues("vip", "premium"))
doc3.AddConjunction(c3)

docs := []*be_indexer.Document{doc1, doc2, doc3}
```

同一个 Conjunction 的同一个字段最多只能有一个 Include Constraint；多个等值应放入一个 Constraint，范围交集应使用 `Between`。

## 3. 构建并查询单个 Segment

```go
// Build
buf := new(bytes.Buffer)
err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})

// Load
reader, err := be_indexer.NewSegmentReader(buf.Bytes())
engine, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})

// Query
result, err := engine.Retrieve(be_indexer.Assignments{
    "age":  []int{20},
    "city": []string{"beijing"},
})
// result → BitmapDocSet containing DocID 1 and 2
```

## 4. 生产模式：Full + Delta + mmap

生产环境中推荐离线构建，再通过 mmap 加载不可变 Segment。

### Build

```go
// Full index (daily batch)
fullOpt := be_indexer.FullIndexBuildOptions{
    Root:       "/data/index",
    Generation: 20240701,
    Fields:     fields,
}
full, err := be_indexer.NewFullIndexBuilder(fullOpt)
if err != nil { return err }
defer full.Close()
for _, doc := range allDocs {
    if err := full.AddDocument(doc); err != nil {
        return err
    }
}
fullDesc, err := full.Build()
// → /data/index/full/full-20240701/segment-000000.bei ...
```

```go
// Delta index (incremental, every 5 min)
deltaOpt := be_indexer.DeltaIndexBuildOptions{
    Root:                   "/data/index",
    Generation:             202407010001,
    FromWatermarkExclusive: 0,
    ToWatermarkInclusive:   snapshotWatermark,
    Fields:                 fields,
}
delta, err := be_indexer.NewDeltaIndexBuilder(deltaOpt)
if err != nil { return err }
defer delta.Close()
for _, m := range mutations { // Mutation{Op: MutationUpsert/MutationDelete, DocID, Version, Document}
    if err := delta.AddMutation(m); err != nil { return err }
}
deltaDesc, err := delta.Build()
// → /data/index/delta/delta-202407010001/segment-000000.bei
//                                      changed_docs.bin
//                                      deleted_docs.bin
```

```go
// Publish manifest
manifest, err := be_indexer.NewSnapshotManifest(be_indexer.SnapshotManifestRequest{
    IndexName:  "targeting",
    Generation: 202407010001,
    Fields:     fields,
    Full:       fullDesc,
    Deltas:     []be_indexer.DeltaIndexDescriptor{deltaDesc},
})
be_indexer.PublishManifest("/data/index", "manifest-1.json", manifest)
// → /data/index/manifests/manifest-1.json
// → /data/index/CURRENT → "manifests/manifest-1.json"
```

### Serve

```go
// Cold start
engine, err := be_indexer.OpenIndex("/data/index", fields,
    be_indexer.LoaderOptions{SegmentLoad: be_indexer.SegmentLoadMmapVerify},
)
results, err := engine.Retrieve(assignments)
```

mmap 模式默认按 Manifest 验证文件尺寸和整文件 SHA-256，再检查 Segment 结构与 block 边界。若发布链路已经完成可信校验，且需要避免冷启动扫描所有数据页，可以显式开启：

```go
be_indexer.LoaderOptions{
    SegmentLoad: be_indexer.SegmentLoadMmapTrustPublished,
}
```

快速模式仍校验尺寸、结构、边界和 checksum 元数据格式，但不重新计算 payload 哈希。

```go
// With live reload
holder, err := be_indexer.NewIndexHolder("/data/index", fields,
    be_indexer.LoaderOptions{SegmentLoad: be_indexer.SegmentLoadMmapVerify},
)
if err != nil {
    return err
}
defer holder.Close()

// 由配置通知或业务 watcher 触发 reload。失败时旧快照继续服务。
if err := holder.Reload(); err != nil {
    return err
}

// All readers get the latest snapshot
results, err := holder.Retrieve(assignments)
```

## 5. 多 Segment 构建

拆分 Segment 可以降低单段构建峰值；本入口仍持有完整 docs，流式输入请用 FullIndexBuilder。writer 由调用方关闭：

```go
var files []*os.File
defer func() { for _, f := range files { _ = f.Close() } }()
segCount, err := be_indexer.BuildSegments(
    func(segIdx int) (io.Writer, error) {
        f, err := os.Create(fmt.Sprintf("segment_%d.bei", segIdx))
        if err != nil { return nil, err }
        files = append(files, f)
        return f, nil
    },
    fields,
    docs,
    be_indexer.BuildSegmentsOptions{MaxDocsPerSegment: 1_000_000},
)
```

`BuildSegments` 会先检查整个输入集合的 DocID 唯一性，再创建任何 Segment writer；跨 Segment 的重复 DocID 也会被拒绝。

## 6. 可观测性

```go
type obs struct{ matchCount int }

func (o *obs) OnRetrieveStart(ctx *be_indexer.RetrieveContext) {}
func (o *obs) OnRetrieveEnd(ctx *be_indexer.RetrieveContext)   {}
func (o *obs) OnConjunctionMatch(docID be_indexer.DocID, conjID be_indexer.ConjID)   { o.matchCount++ }
func (o *obs) OnExcludeSkip(docID be_indexer.DocID)                       {}
func (o *obs) OnCursorInit(fieldCount int)                                {}

results, _ := engine.Retrieve(assignments,
    be_indexer.WithObserver(&obs{}),
)
```

## 7. 索引合并与全量发布评估

根包 `api_maintenance.go` 只提供业务评估参考，不自动触发任何任务。推荐一个 full + 一个累计 delta，每次发布替换旧 delta，各自可包含多个 segment：

```go
stats := be_indexer.CollectIndexMaintenanceStats(manifestValue)
decision := be_indexer.EvaluateIndexMaintenanceStats(stats, be_indexer.IndexMaintenanceOptions{})

switch decision.Decision {
case be_indexer.IndexMaintenanceDecisionRebuildFull:
    // 业务评估是否重新构建并发布 full。
case be_indexer.IndexMaintenanceDecisionRebuildDelta:
    // 业务评估是否合并或重建 delta。
case be_indexer.IndexMaintenanceDecisionNone:
    // 未超过配置的阈值，保持当前发布计划。
}
```

此例只使用 Manifest 统计。若要按距 full 构建完成经过的时间评估，业务需自行记录构建完成时间并通过 `IndexMaintenanceRuntimeObservation.TimeSinceFullBuild` 传入；库不会自动计算。具体调用方职责见[最佳实践](best-practices.md#10-索引合并与全量发布评估)，阈值和统计口径见 [API 参考](api-reference.md#索引合并与全量发布评估)。

## 8. 性能原则

项目优先保证规则表达能力、正确性和线上可靠性，再以端到端吞吐、P99、内存峰值和冷启动时间衡量性能。mmap zero-copy 表示大型 posting/wildcard 数据无需复制到 Go heap；它不要求完整 Retrieve 绝对零分配。

---

## 当前 API

| 能力 | API |
|:-----|:----|
| 内存数据读取 | `segment.NewSegmentReader(data)` |
| mmap 文件读取 | `segment.OpenSegmentFile(path, options)` |
| 构建查询引擎 | `engine.NewBooleanEngine(fields, readers)` |
| wildcard | 内嵌于 Segment v5，由引擎直接读取 |

当前 API 不再暴露独立 wildcard sidecar 或额外 wildcard 参数，避免同一份数据出现两个来源。

---

## 延伸阅读

- [BEST_PRACTICES.md](best-practices.md) — 生产接入指南：规则建模语义、容器选型、full+delta 链路、热更新与反模式。
- [ARCHITECTURE.md](../architecture/overview.md) — 分层设计与检索算法。
- [indexes/README.md](../architecture/indexes/README.md) — 各索引容器选型参考。
