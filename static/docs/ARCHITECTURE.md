# 架构设计

本文描述 be_indexer 的内部架构、核心算法、物理布局和扩展机制。设计目标按优先级依次是：布尔语义正确、索引能力完整、线上生命周期可靠，以及在真实负载下获得高吞吐和低延迟。mmap zero-copy、低分配和对象复用是实现性能目标的手段。

## 1. Layered Architecture

<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; max-width: 420px; margin: 20px 0;">
  <div style="display: flex; flex-direction: column; align-items: center; gap: 4px;">
    <div style="background: #ffeecc; border: 2px solid #d4a04a; border-radius: 6px; padding: 8px 28px; font-weight: 600;">Application</div>
    <div style="color: #666; font-size: 16px;">↓ &nbsp; ↓ &nbsp; ↓</div>
    <div style="display: flex; gap: 10px;">
      <div style="background: #d4edda; border: 1px solid #28a745; border-radius: 6px; padding: 6px 12px; text-align: center; font-size: 12px;">
        <div style="font-weight: 600;">builder</div><div style="color: #666;">offline</div>
      </div>
      <div style="background: #d6eaf8; border: 1px solid #2e86c1; border-radius: 6px; padding: 6px 12px; text-align: center; font-size: 12px;">
        <div style="font-weight: 600;">loader</div><div style="color: #666;">cold start</div>
      </div>
      <div style="background: #d1ecf1; border: 1px solid #17a2b8; border-radius: 6px; padding: 6px 12px; text-align: center; font-size: 12px;">
        <div style="font-weight: 600;">engine</div><div style="color: #666;">online</div>
      </div>
    </div>
    <div style="color: #666; font-size: 16px;">↓ &nbsp; ↓</div>
    <div style="background: #f5deb3; border: 1px solid #d4a04a; border-radius: 6px; padding: 6px 20px; text-align: center; font-size: 12px;">
      <div style="font-weight: 600;">segment</div><div style="color: #666;">mmap, r/o</div>
    </div>
    <div style="color: #666; font-size: 16px;">↓</div>
    <div style="background: #f0e0e0; border: 1px solid #c08080; border-radius: 6px; padding: 6px 20px; text-align: center; font-size: 12px;">
      <div style="font-weight: 600;">core</div><div style="color: #666;">types</div>
    </div>
  </div>
</div>

### 1.0 分层与依赖方向

依赖单向向下（上层依赖下层，下层不感知上层）。`core` 处于最底层，仅定义类型与接口，无业务逻辑。

```
be_indexer (公共 API / 类型重导出 / 工厂函数)
    └─> loader · manifest · compact   (装配层：冷启动、快照、压实策略)
            └─> engine                 (执行层：BooleanEngine / CompositeEngine)
                    ├─> builder        (构建层：DNF 文档 → 二进制段)
                    └─> parser         (解析层：SchemaCodec / Encoder / Tokenizer)
                            └─> segment (存储层：Builder 序列化 / SegmentReader 反序列化)
                                    └─> core (基础层：类型 + 接口 ONLY)
```

| Layer | 包 | 职责 |
|-------|----|------|
| 公共 API | `be_indexer.go` | 类型重导出、工厂函数 (NewEngine / BuildSegment / OpenIndex) |
| 装配 | `loader` `manifest` `compact` | 冷启动、CURRENT→快照、full+delta 合成、压实决策 |
| 执行 | `engine` | `mergeCursors` K-Groups 归并、CompositeEngine |
| 构建 | `builder` | DNF 文档 → 二进制段 (pull / push / delta) |
| 解析 | `parser` | SchemaCodec、Encoder.Build/Query、Tokenizer |
| 存储 | `segment` | FlatDict / FlatPostingList / 容器注册表 / mmap 零拷贝 |
| 基础 | `core` | ConjID/EntryID、Document/Conjunction、FieldCursor、接口 |

### 1.1 core — Foundation Types

`core` 提供最底层类型、ID 编解码、游标和结果集合，不依赖上层业务包。

```go
type BEField  string            // Field identifier
type DocID    int64             // Document ID (−2^43 to 2^43)
type ConjID   uint64            // Conjunction identity (packed)
type EntryID  uint64            // Posting list entry (packed)
```

Key interfaces:

```go
type PostingIterator interface {   // Single posting list cursor
    Current() EntryID
    SkipTo(target EntryID) EntryID
    Term() Term
    ReachEnd() bool
}
type ResultCollector interface { Add(id DocID) }
type RetrieveObserver interface { ... }   // Instrumentation hooks
```

PostingIterator 的两个实现：
- `SliceIterator` — wraps `[]EntryID` with galloping (exponential+)binary search SkipTo
- `flatPostingCursor` — mmap zero-copy view with same galloping SkipTo

### 1.2 segment — Physical Storage

#### File Layout

<div style="font-family: 'SF Mono', 'Fira Code', monospace; font-size: 12px; margin: 20px 0; line-height: 1.6;">
  <div style="display: flex; flex-direction: column; gap: 0; max-width: 560px;">
    <div style="background: #e0e0e0; border: 1px solid #999; border-radius: 4px; padding: 6px 12px; font-weight: 600;">
      MagicNumber &nbsp; <code>"BEIDX\0\0\5"</code> <span style="color: #888;">(8B)</span>
    </div>
    <div style="text-align: center; color: #999;">▼</div>
    <div style="border: 2px solid #666; border-radius: 6px; padding: 10px 12px; background: #f8fdf8;">
      <div style="font-weight: 600; margin-bottom: 6px;">Per Field <span style="font-weight: 400; color: #666;">(sorted)</span></div>
      <div style="display: flex; gap: 8px; flex-wrap: wrap;">
        <div style="background: #d4edda; border: 1px solid #28a745; border-radius: 3px; padding: 4px 8px; font-size: 11px;">
          field_postings<br>FlatPostingList
        </div>
        <div style="background: #d4edda; border: 1px solid #28a745; border-radius: 3px; padding: 4px 8px; font-size: 11px;">
          field_dict<br>FlatDict
        </div>
        <div style="background: #e8e0f0; border: 1px solid #9b59b6; border-radius: 3px; padding: 4px 8px; font-size: 11px;">
          field_ac_matcher<br>Double-Array Trie
        </div>
        <div style="background: #e8e0f0; border: 1px solid #9b59b6; border-radius: 3px; padding: 4px 8px; font-size: 11px;">
          field_ext_range<br>Segment-Tree
        </div>
      </div>
    </div>
    <div style="text-align: center; color: #999;">▼</div>
    <div style="background: #d1ecf1; border: 1px solid #17a2b8; border-radius: 4px; padding: 6px 12px;">
      __wildcards &nbsp; <span style="color: #666;">BEIENT1 (16B hdr + M×8B)</span>
    </div>
    <div style="text-align: center; color: #999;">▼</div>
    <div style="background: #fff3cd; border: 1px solid #ffc107; border-radius: 4px; padding: 6px 12px;">
      MetaBlock &nbsp; <span style="color: #666;">(JSON)</span>
    </div>
    <div style="text-align: center; color: #999;">▼</div>
    <div style="background: #e0e0e0; border: 1px solid #999; border-radius: 4px; padding: 6px 12px;">
      MetaOffset &nbsp; <span style="color: #666;">(uint64 LE, 8B)</span>
    </div>
  </div>
  <div style="margin-top: 8px; color: #666; font-size: 11px; font-style: italic;">
    8-byte aligned block → zero-copy unsafe.Slice
  </div>
</div>

#### Zero-Copy Pipeline

```mermaid
flowchart TD
    A[Segment file] -->|mmap| B["[]byte backing\n(mmap or heap)"]
    B --> C["checkedBlockBytes\n(bounds + checksum)"]
    B --> D["mapEntryIDs\nalignment check"]
    D -->|"&7 == 0"| E["unsafe.Slice (0-copy)"]
    D -->|"&7 != 0"| F["fallback copy"]
    C --> G["SegmentReader\nholds mmap ref"]
    E --> G
    F --> G
```

- Posting blocks: 8-byte aligned → `mapEntryIDs` does `unsafe.Slice` → zero-copy `[]EntryID` view
- Wildcards block: 8-byte aligned → `decodeEntriesBlock` uses `mapEntryIDs` → zero-copy view
- `SegmentReader.Wildcards()` returns the view directly (no defensive copy)
- Old segments (misaligned): `mapEntryIDs` detects `&7 != 0` → fallback copy

#### Containers

可插拔索引通过注册表接入：

```go
segment.RegisterIndex(kind, segment.IndexDef{
    Reader:  readerFactory,
    Builder: builderFactory,
})
```

Built-in containers:
- `ac_matcher` — Double-Array Trie for multi-pattern substring match
- `ext_range` — segment-tree for numeric range queries (GT, LT, BETWEEN)

### 1.3 builder — Document Compiler

Converts `[]*core.Document` into binary segments.

#### Build Paths

| Path | Builder | When |
|:-----|:--------|:-----|
| Pull (in-memory) | `BuildSegmentFromDocs` → `InMemorySegmentBuilder` | Tests, small docs, single segment |
| Pull (multi-seg) | `BuildSegmentsFromDocs` → splits by `MaxDocsPerSegment` | Medium workloads |
| Push (streaming) | `FullIndexBuilder` → `ExternalBuilder` | Full index, external sort, spill to disk |
| Push (delta) | `DeltaIndexBuilder` → `InMemorySegmentBuilder` | Delta index, in-memory compact |

#### Document Export Flow

```
Document
  └── Conjunction (DNF clause)
       ├── K = CalcConjSize() → 0 = wildcard
       └── Predicate (per field)
            └── Encoder.Build → []Posting{Term, EntryID}
                 └── sink.AddPosting(K, field, term, [EntryID])
```

The `EntryID` encodes the ConjID with include/exclude bit. Postings are grouped by field, sorted by EntryID (which sorts by K → index → DocID).

### 1.4 engine — Query Evaluation

#### BooleanEngine

```
Retrieve(assignments)
  │
  ├── encodeQueries(assignments) → []encodedField
  │     └── schemaCodec.Query(values) → []EncodedQuery{Kind, Term}
  │
  ├── initCursors(encoded)
  │     ├── Per-segment wildcards → SliceIterator → FieldCursor (lazy heap merge)
  │     └── Per-field: segment.GetPostingsByTerm/GetRangePostings/MultiPatternSearch
  │                    → flatPostingCursors → FieldCursor
  │
  └── mergeCursors(ctx, fieldCursors)
        └── Single-pass multiway merge, K read from EntryID dynamically
```

`initCursors` 通过字段 Schema 的 `IndexType` 调用统一的 `SegmentReader.IndexQuery`，再由已注册的
`IndexReader.MatchQuery` 完成 default、range、AC 或自定义容器查询。所有路径返回的
`PostingIterator` 都封装进 `FieldCursor`，由内部 min-heap 做懒归并。

#### mergeCursors Algorithm

```
sorted cursors by current EntryID (min first):
  eid = peek()
  K = eid.GetConjID().Size()
  needMatchCnt = max(K, 1)

  if first 'needMatchCnt' cursors at same ConjID:
    if include → collect DocID
    if exclude → skip ALL remaining cursors past this DocID
  advance first needMatchCnt cursors via SkipTo(nextID)
  compact exhausted cursors
```

Key properties:
- K=0 wildcards need only 1 cursor to match (themselves)
- Exclude short-circuit: when K cursors match but the K-th is exclude, skip all
- All cursors compete in a single round-robin — natural interleaving of fields and segments

#### CompositeEngine

Orchestrates full + N delta engines:

```
Result = (FullResult - ChangedDocs) ∪ (Δ₀ - DeletedDocs₀) ∪ (Δ₁ - DeletedDocs₁) ∪ ...
```

- Batch bitmap operations (AndNot, Or) — no per-document loops
- Each engine queries independently, results merged

### 1.5 loader — Cold Start

Reads `index_root/CURRENT` → `manifest.json` → loads full + delta segments → constructs CompositeEngine snapshot.

```
OpenIndex(root, fields, opts)
  ├── ReadCurrentManifest(root) → Manifest
  ├── loadFullEngine(root, fields, full, opts)
  │     ├── loadSegments → []*SegmentReader
  │     └── NewBooleanEngine(fields, segments)  // wildcards from segments directly
  ├── loadDeltas(root, fields, deltas, opts)
  │     ├── Per delta: loadSegments, changed_docs, deleted_docs
  │     ├── Build BooleanEngine × N (with LiveDocs for dedup across deltas)
  │     └── Collect ChangedDocs, DeletedDocs
  └── IndexSnapshot{FullEngine, DeltaEngines, ChangedDocs, DeletedDocs}
       └── CompositeEngine(snapshot)
```

`SegmentLoadMmapVerify` 默认先按 Manifest 顺序计算整文件 SHA-256，再 mmap 文件并为 posting/wildcard 建立 zero-copy 视图。只有显式选择 `SegmentLoadMmapTrustPublished` 才跳过内容哈希扫描。

加载采用“临时拥有，成功后移交”的资源模型：

```mermaid
flowchart TD
    A[读取 CURRENT 和 Manifest] --> B[加载并校验 Full]
    B --> C[加载 Delta、changed/deleted sidecar]
    C --> D{全部成功?}
    D -->|是| E[构造不可变 IndexSnapshot]
    E --> F[Holder 发布新快照]
    D -->|否| G[关闭已创建 Engine 和 mmap]
    G --> H[返回错误，旧快照继续服务]
```

正常 reload 时，Holder 先发布新 `refSnapshot`，再 retire 旧快照；旧 mmap 在最后一个在途查询 release 后关闭。

### 1.6 compact — Compaction Policy

Monitors full age and delta accumulation, recommends compaction:

```go
stats := be_indexer.CollectCompactStats(manifestValue)
recommendation := be_indexer.DecideCompactStats(stats, be_indexer.CompactOptions{})
// → Recommendation{Decision: DecisionMajor/DecisionMinor/DecisionNone, Reasons: ...}
```

---

## 2. Core Algorithms

### 2.1 ConjID / EntryID Encoding

K（一个 Conjunction 中 Include 约束的数量）编码在 `EntryID` 的高位 (bits 56–63)，
使排序后的 EntryID 天然按 K 分组，无需物理分桶；查询时通过 `conjID.Size()` 动态读取 K。

```
ConjID (64 bit):
┌─────────┬──────────┬──────────┬──────────┬───────────────────────┐
│reserve 4│ size K 8 │ index 8  │ negSign 1│ docID 43              │
└─────────┴──────────┴──────────┴──────────┴───────────────────────┘
 bits 63-60  bits 59-52 bits 51-44  bit 43     bits 42-0

EntryID = (ConjID << 4) | incl/excl
┌──────────────┬───────────────────────────────┬────────┬──────┐
│  K 8 bit     │  ConjID 移入 (bits 4..63)     │ empty 3│ I/E  │
└──────────────┴───────────────────────────────┴────────┴──────┘
 bits 63-56    bits 55-4                        bits 3-1  bit 0
   ▲ K 在此 → KStartEntryID(k) = k << 56
   I/E = 0 排除(excl) / 1 包含(incl)
```

`KStartEntryID(k)` 返回 `k << 56`，区间 `[KStartEntryID(k), KStartEntryID(k+1))` 即同一 K 组。
当前检索路径不再按 K 创建 posting 子视图，而是在 `mergeCursors` 中从 EntryID 动态读取 K。

### 2.2 Galloping SkipTo

Applied to both `SliceIterator` (wildcards) and `flatPostingCursor` (mmap postings). Replaces full binary search with:

```
SkipTo(target):
  if current >= target → return current        // fast path, O(1)
  lo, step = cursor+1, 1
  while data[cursor+step] < target:            // exponential probe
    lo = cursor + step + 1; step *= 2
  binary search within [lo, min(cursor+step, n-1)]  // narrow window
```

~90% of calls are "advance to next entry" → fast path hits → 1 comparison instead of 17 (log N).

### 2.3 Wildcard Lazy Merge

Each segment's `__wildcards` block is independently sorted at build time. At query time, per-segment `SliceIterator`s are grouped into one `FieldCursor` whose internal min-heap merges lazily:

```
Instead of:
  load: merge all segments' wildcards → sorted []EntryID (O(N log K) + allocation)
  query: one SliceIterator

Now:
  load: nothing
  query: per-segment SliceIterator → FieldCursor heap (same as field cursor pattern)
```

Zero load-time cost. The FieldCursor's heap does exactly what the K-way merge would — just amortized across queries instead of paid upfront.

---

## 3. Full + Delta Snapshot Model

### 3.1 Directory Layout

```
index_root/
  ├── CURRENT              → "manifests/manifest-000001.json"
  ├── manifests/
  │   └── manifest-000001.json
  ├── full/
  │   └── full-20240701/
  │       ├── segment-000000.bei      (mmap segment, v5)
  │       ├── segment-000001.bei
  │       └── segment-000002.bei
  └── delta/
      └── delta-202407010001/
          ├── segment-000000.bei
          ├── changed_docs.bin        (DocID sidecar, "BEIDOC1")
          └── deleted_docs.bin
```

### 3.2 Manifest

```json
{
  "manifest_version": 1,
  "index_name": "ad_targeting",
  "generation": 20240701,
  "schema_hash": "sha256:...",
  "format_version": "segment-v5",
  "full": {
    "path": "full/full-20240701",
    "segments": [{"segment_id": 0, "file": "segment-000000.bei", ...}]
  },
  "deltas": [{
    "path": "delta/delta-202407010001",
    "segments": [{"segment_id": 0, "file": "segment-000000.bei", ...}],
    "changed_docs_file": "changed_docs.bin",
    "deleted_docs_file": "deleted_docs.bin"
  }]
}
```

### 3.3 Query Semantics

```
Result = (FullResult − ChangedDocs) ∪ Σ(DeltaResult − DeletedDocs)
```

Manifest 文件按引用 create-once，不允许同名覆盖。`Holder.Reload` 对 CURRENT 只读取一次；若引用与当前已加载引用相同则直接 no-op，否则按该精确引用构建新快照后原子发布。

- `ChangedDocs` = union of all delta changed_docs — removes stale full entries
- `DeletedDocs` = union of all delta deleted_docs — removes deleted entries
- Later deltas override earlier deltas for same DocID (via LiveDocs)
- All operations are batch bitmap (AndNot, Or) — no per-document loop

---

## 4. Extension Points

### 4.1 自定义索引容器

```go
type IndexBuilder interface {
    AddRecord(record any, entries []core.EntryID) error
    Build(writer BlockWriter) error
}
type IndexReader interface {
    MatchQuery(ctx BlockContext, field core.BEField, query any) ([]core.PostingIterator, error)
}

func init() {
    segment.RegisterIndex("my_index", segment.IndexDef{
        Reader:  func(data []byte) (segment.IndexReader, error) { return newMyReader(data) },
        Builder: func(env segment.BuilderEnv) segment.IndexBuilder { return newMyBuilder(env) },
    })
}
```

### 4.2 自定义 Predicate Encoder

```go
type PredicateEncoder interface {
    Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error)
    Query(value any) ([]parser.EncodedQuery, error)
}

func init() {
    parser.RegisterPredicateEncoder("my_encoder", factory)
}
```

### 4.3 Extended Segment Building

For very large workloads (> 10M docs), use `ExternalBuilder` directly with external sort:

```go
builder := segment.NewExternalBuilder(writer, runDir, segment.ExternalBuilderOptions{
    MaxPostingsInMemory: 5_000_000,
})
// Add docs via exportDocToSink pattern
```

超过 `MaxPostingsInMemory` 后会写 sorted run。该阈值控制当前 distinct key 记录数；单个超热 key 的 EntryID 列表、最终字典和文档级编码缓冲仍会占用内存，因此应结合真实 key 分布压测峰值。

---

## 5. Key Design Decisions (设计要点)

| 角度 | 决策 | 收益 |
|------|------|------|
| 读写分离 | `builder` 离线产出 `segment`，`engine` 只读 mmap 段 | 构建可重放，查询无锁、并发安全 |
| mmap 零拷贝 | `mmap` + `unsafe.Slice` 直接映射 `EntryID[]` | 避免复制大型 posting/wildcard 数据；完整 Retrieve 可保留必要的受控临时分配 |
| K 高位编码 | K 编入 `EntryID[56:63]` | 排序即分组，免物理分桶；归并时动态读取 K |
| 可插拔容器 | `RegisterIndex` + `IndexReader/IndexBuilder` | `ac_matcher`/`ext_range` 及自定义容器按统一接口扩展 |
| Wildcard 短路 | K=0 单列 Z-Entry，归并时永远命中 | 无约束文档走独立快路径 |
| Exclude 优化 | `ShortCircuitAfter` 跳过后续游标 | 避免排除命中导致的误判匹配 |
| 增量索引 | `manifest`(CURRENT)+ `delta` 段 + LiveDocs | full/delta 合成 `CompositeEngine`，支持在线更新 |
| 块对齐 | 8 字节对齐 + SHA-256 block 校验 | 保证当前 little-endian 平台上的零拷贝映射合法，并检测内容损坏 |

### 5.1 DocID 唯一性边界

DocID 被编码进 ConjID，因此必须在整个 full corpus 内唯一，而不只是单个 Segment 内唯一：

```mermaid
flowchart LR
    A[完整 Documents 输入] --> B{全局 DocID 去重校验}
    B -->|重复| C[构建失败，不创建 Segment writer]
    B -->|唯一| D[按 MaxDocsPerSegment 切分]
    D --> E[Segment 0]
    D --> F[Segment 1]
    D --> G[Segment N]
```

`BuildSegmentsFromDocs` 会在多段构建开始前做全局校验；流式 `FullIndexBuilder` 使用跨 Segment 的 roaring64 seen-set。这样可防止相同 DocID 的 posting 在不同 Segment 中合并为错误的 Conjunction。

### 5.2 完整性与冷启动性能

| 加载模式 | 校验行为 | 使用建议 |
|---|---|---|
| `SegmentLoadHeapVerify` | 读取到 heap，校验 manifest 文件尺寸和整文件 SHA-256 | 测试、小文件或必须加载进堆的场景 |
| `SegmentLoadMmapVerify` | 同一文件句柄校验尺寸和整文件 SHA-256，再 mmap | 生产推荐，覆盖数据块、metadata 和 footer |
| `SegmentLoadMmapTrustPublished` | 同一文件句柄校验尺寸后 mmap，再校验结构与边界 | 仅当发布链路已可信校验，且冷启动扫描成本不可接受时使用 |

快速启动通过 `SegmentLoad: SegmentLoadMmapTrustPublished` 开启。它不会重新计算文件哈希，因此这是显式的完整性与启动性能取舍。

### 5.3 性能口径

- 功能正确性、规则表达能力和端到端性能优先。
- mmap zero-copy 描述的是大型索引数据不复制到 Go heap，并不等价于整个 Retrieve 没有任何分配。
- `SkipTo` 等核心循环应保持零分配；查询编码、迭代器组合和独立结果所有权可以有受控分配。
- 优化决策应同时观察吞吐、P99、内存峰值、冷启动时间和 Shadow Testing 结果。
