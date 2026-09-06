# 快速开始

## 1. 定义 Schema

Define which fields to index and how values are tokenized:

```go
import "github.com/echoface/be_indexer"

fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "age": {
        ID:    1,
        Field: "age",
        FieldOption: be_indexer.FieldOption{
            IndexType: be_indexer.IndexNameDefault,
            Encoder:   "number",
        },
    },
    "city": {
        ID:    2,
        Field: "city",
        FieldOption: be_indexer.FieldOption{
            IndexType: be_indexer.IndexNameDefault,
            Encoder:   "default",
        },
    },
}

// For range queries ("age > 18"):
fields["age"].FieldOption.IndexType = be_indexer.IndexNameExtendRange
fields["age"].FieldOption.Encoder = be_indexer.IndexNameExtendRange

// For substring matching ("tag contains 'premium'"):
fields["tag"].FieldOption.IndexType = be_indexer.IndexNameACMatcher
fields["tag"].FieldOption.Encoder = be_indexer.IndexNameACMatcher
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
fullOpt := be_indexer.FullIndexBuildOption{
    Root:       "/data/index",
    Generation: 20240701,
    Fields:     fields,
    Options: be_indexer.BuildDirectoryOptions{
        SegmentSchemaHash: "sha256:your-schema-hash",
    },
}
full, err := be_indexer.NewFullIndexBuilder(fullOpt)
for _, doc := range allDocs {
    if err := full.AddDocument(doc); err != nil {
        // handle
    }
}
fullDesc, err := full.Build()
// → /data/index/full/full-20240701/segment-000000.bei ...
```

```go
// Delta index (incremental, every 5 min)
deltaOpt := be_indexer.DeltaIndexBuildOption{
    Root:                   "/data/index",
    Generation:             202407010001,
    FromWatermarkExclusive: 0,
    ToWatermarkInclusive:   snapshotWatermark,
    Fields:                 fields,
    Options: be_indexer.BuildDirectoryOptions{
        SegmentSchemaHash: "sha256:your-schema-hash",
    },
}
delta, err := be_indexer.NewDeltaIndexBuilder(deltaOpt)
for _, m := range mutations { // Mutation{Op: Upsert/Delete, DocID, Doc}
    delta.AddMutation(m)
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
    SchemaHash: "sha256:your-schema-hash",
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

For large datasets, split across segments to bound peak memory:

```go
segCount, err := be_indexer.BuildSegments(
    func(segIdx int) (io.Writer, error) {
        return os.Create(fmt.Sprintf("segment_%d.bei", segIdx))
    },
    fields,
    docs,
    be_indexer.BuildOptions{MaxDocsPerSegment: 1_000_000},
)
```

`BuildSegments` 会先检查整个输入集合的 DocID 唯一性，再创建任何 Segment writer；跨 Segment 的重复 DocID 也会被拒绝。

## 6. 可观测性

```go
type obs struct{ matchCount int }

func (o *obs) OnRetrieveStart(ctx *be_indexer.RetrieveContext) {}
func (o *obs) OnRetrieveEnd(ctx *be_indexer.RetrieveContext)   {}
func (o *obs) OnMatch(docID be_indexer.DocID, conjID be_indexer.ConjID)   { o.matchCount++ }
func (o *obs) OnExcludeSkip(docID be_indexer.DocID)                       {}
func (o *obs) OnCursorInit(fieldCount int)                                {}

results, _ := engine.Retrieve(assignments,
    be_indexer.WithObserver(&obs{}),
)
```

## 7. Compaction

Monitor index health and trigger merges:

```go
stats := be_indexer.CollectCompactStats(manifestValue)
decision := be_indexer.DecideCompactStats(stats, be_indexer.CompactOptions{})

switch decision.Decision {
case be_indexer.CompactDecisionMajor:
    // Full rebuild needed (many deltas / old full)
case be_indexer.CompactDecisionMinor:
    // Merge deltas (reduce delta count)
case be_indexer.CompactDecisionNone:
    // Healthy
}
```

## 8. 性能原则

项目优先保证规则表达能力、正确性和线上可靠性，再以端到端吞吐、P99、内存峰值和冷启动时间衡量性能。mmap zero-copy 表示大型 posting/wildcard 数据无需复制到 Go heap；它不要求完整 Retrieve 绝对零分配。

---

## 当前 API

| 能力 | API |
|:-----|:----|
| 内存数据读取 | `segment.NewSegmentReader(data)` |
| mmap 文件读取 | `segment.OpenSegmentFile(path, options)` |
| 构建查询引擎 | `engine.NewBooleanEngine(fields, readers)` |
| wildcard | 内嵌于 Segment v4，由引擎直接读取 |

当前 API 不再暴露独立 wildcard sidecar 或额外 wildcard 参数，避免同一份数据出现两个来源。
