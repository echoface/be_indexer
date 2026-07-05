# K-Group Elimination: Compact Index Architecture

Status: **Draft** | Branch: `compact-merge` | Date: 2026-07-05

## 1. Motivation

EntryID 编码将 K (conjunction size) 置于高 8 位 (bits 56-63)。对 EntryID 升序排序后，所有相同 K 的条目天然连续——K 不需要成为存储维度。当前 next 分支在 segment 层按 `(K, field)` 物理分桶 (`blockKey = K<<16 | fieldID`)，是人为重复这一信息，导致：

- 字典条目膨胀 30×（每个 K 级独立 dict）
- Block 碎片化（≈ `|K| × |fields|` 个小块 vs `|fields|` 个大块）
- 检索路径需要 `maxK+1` 次 `initCursors` 重复调用
- 外部构建器三级分组 `(K, field, term)` 增加复杂度

master 分支的 `CompactBEIndex` 已验证合并方案的可行性：所有 K 的 EntryID 混存于同一容器，
EntryID 自然排序隐式完成 K 分组，一次 `initCursors` 替代 K 循环。

## 2. Design Overview

### 2.1 核心思想

K 是 EntryID 的**属性**，不是**存储维度**。segment 按 field 组织，dict 和 posting list 合并所有 K。

```
Before（K-Grouped）:
  Segment
    ├── K=0: [fieldA dict] [fieldA pl] [fieldB dict] [fieldB pl] ...
    ├── K=1: [fieldA dict] [fieldA pl] [fieldB dict] [fieldB pl] ...
    └── K=2: ...                            ← block ≈ |K| × |fields|

After（Merged）:
  Segment
    ├── fieldA: [dict (all K)] [postings (all K, sorted by EntryID)]
    ├── fieldB: [dict (all K)] [postings (all K, sorted by EntryID)]
    └── ...                                  ← block ≈ |fields|
```

### 2.2 二分定位 K 边界

EntryID 高位即 K，推导 K 的起始 EntryID 边界：

```
EntryID 布局:
  bits 63-56: K (conjunction size, 0-255)
  bits 55-0:  index | negSign | docID | empty | incl/excl

KStartEntryID(k) = EntryID(uint64(k) << 56)
// K=3 区间: [KStartEntryID(3), KStartEntryID(4))
// EntryID 升序 → K=0, K=1, K=2, ... 自然递增
```

查询时二分定位 posting list 中的 K 子区间，零拷贝视图：

```go
pl := dict.Find(term)                    // 全量 PostingRef
kStart := sort.Search(pl.Count, KStartEntryID(k) <= pl.EntryID)
kEnd   := sort.Search(pl.Count, KStartEntryID(k+1) <= pl.EntryID)
subPL  := pl.SubView(kStart, kEnd)       // 零拷贝子视图
```

### 2.3 检索算法切换

```
Before (K-Grouped):
  for k := maxK; k >= 0; k--     ─── 外层 K 循环
    initCursors(k)               ─── 每个 K 重建游标
    retrieveK(k)                 ─── needMatchCnt = k

After (Compact):
  initCursorsOnce()              ─── 一次建游标，无 K 参数
  retrieveAll()                  ─── K 从 EntryID 动态读
    stepK := conjID.Size()
    needMatchCnt := max(1, stepK)
```

`retrieveAll` 流程：

1. `fieldCursors.Sort()` （EntryID 升序 → 隐式 K 升序处理）
2. peek min EntryID → `stepK = Size()` → `needMatchCnt = max(1, stepK)`
3. 检查前 `needMatchCnt` 个游标是否指向同一 conjID
    - Include → 命中
    - Exclude → 短路跳过
4. 推进前 `needMatchCnt` 个游标
5. `fieldCursors.Sort()` + `CompactLast()` 移除耗尽的游标
6. 若 `needMatchCnt > len(fieldCursors)` → break

## 3. Architecture Changes by Layer

### 3.1 Layer 1: `core`

**Additions:**

`core/id_types.go` — K boundary helper:

```go
// KStartEntryID returns the minimum EntryID for conjunction size K.
// K occupies bits 56-63 of EntryID. For K=3:
//   entries >= KStartEntryID(3) && entries < KStartEntryID(4) are K=3.
func KStartEntryID(k int) EntryID
```

`core/cursor.go` — PostingIterator / FieldCursors extensions:

```go
// FieldCursor: add ReachEnd
func (f *FieldCursor) ReachEnd() bool

// FieldCursors: add CompactLast
func (fcs *FieldCursors) CompactLast()
```

`core/definitions.go` — PostingIterator add method:

```go
type PostingIterator interface {
    Current() EntryID
    SkipTo(EntryID) EntryID
    Term() Term
    // ReachEnd reports whether this iterator has no more entries.
    ReachEnd() bool
}
```

**Removals:** none.

### 3.2 Layer 2: `segment`

**segment/format.go:**

```go
// Before
type blockKey uint32
func makeBlockKey(k int, fieldID uint16) blockKey

// After
type blockKey uint16
func makeBlockKey(fieldID uint16) blockKey
```

**segment/block_name.go:**

```go
// Before
func blockName(k int, field string) string   // "k3_age"
func dictBlockName(k int, field string) string
func acBlockName(k int, field string) string
func rangeBlockName(k int, field string) string

// After — K removed from all signatures
func blockName(field string) string          // "age"
func dictBlockName(field string) string
func acBlockName(field string) string
func rangeBlockName(field string) string
```

**segment/segment_reader.go:**

```go
// BlockDef.K 字段在 Phase 2 先写 0；Phase 6 彻底删除
// blockLoad: maps fieldID → *blockLookup（不再复合 K）

// lookupBlock: K 参数移除
func (sr *SegmentReader) lookupBlock(field BEField) (*blockLookup, bool)

// All K sentinel: k < 0 means "return all entries, no K filtering"
const AllK int = -1

// GetPostingsByTerm: 保留 k 参数，内部做 K-range sub-view
// k >= 0: 二分定位 K-range 子视图
// k == AllK (-1): 返回全量 (engine initCursorsOnce 使用)
func (sr *SegmentReader) GetPostingsByTerm(k int, field BEField, term string) (PostingIterator, error) {
    blk := sr.lookupBlock(field)
    ref := blk.dict.Find(term)
    pl := newPostingListAt(blk.pl, ref)
    if k >= 0 {
        pl = pl.SubViewByK(k)  // 二分定位 + 零拷贝子视图
    }
    return pl.NewPostingCursor(NewTerm(field, term)), nil
}

// GetRangePostings — 同理保留 k 参数，支持 AllK
// MultiPatternSearch — 同理保留 k 参数，支持 AllK
```

**segment/posting_list.go — FlatPostingList 新增：**

```go
func (pl *FlatPostingList) Count() int
func (pl *FlatPostingList) EntryIndex(i int) EntryID
func (pl *FlatPostingList) SubView(start, end int) *FlatPostingList   // 零拷贝子视图
func (pl *FlatPostingList) SubViewByK(k int) *FlatPostingList        // K-range 子视图
```

**segment/segment_builder_mem.go:**

```go
// Before
type InMemorySegmentBuilder struct {
    fieldData map[int]map[string]*FieldData
    rangeData map[int]map[string][]Interval
}

// After — K 从数据结构体中移除
type InMemorySegmentBuilder struct {
    fieldData map[string]*FieldData
    rangeData map[string][]Interval   // merged across K
}

// AddPosting: K 参数保留（调用方兼容），内部忽略 K 维度存储
func (sw *InMemorySegmentBuilder) AddPosting(k int, field, term string, entries []EntryID) error

// AddRangePosting: 同理
func (sw *InMemorySegmentBuilder) AddRangePosting(k int, field string, lo, hi int64, entry EntryID) error
```

**segment/segment_builder_external.go:**

```go
// postingRecord: K 字段移除
type postingRecord struct {
    // K     int           ← 删除
    Field string
    Term  string
    Entry core.EntryID
}

// lessPostingRecord: 按 (Field, Term) 排序
// writeMergedBlocks: 按 (Field, Term) 分组，不再是 (K, Field, Term)
//   finishGroup 触发条件: rec.Field != currentField（K 不再参与分组）
```

### 3.3 Layer 3: `engine`

**engine/searcher.go:**

```go
// Before
func (e *BooleanEngine) RetrieveWithCollector(...) error {
    for k := maxK; k >= 0; k-- {
        fCursors := e.initCursors(k, ...)
        e.retrieveK(&ctx, fCursors, k)
    }
}

// After
func (e *BooleanEngine) RetrieveWithCollector(...) error {
    encoded := e.encodeQueries(queries)
    fCursors := e.initCursorsOnce(encoded, ctx.Observer)  // ← 一次
    if fCursors.Len() == 0 { return nil }
    e.retrieveAll(&ctx, fCursors)
}

// initCursorsOnce: 去掉 K 参数，调用 seg.GetPostingsByTerm(segment.AllK, field, term)
//    (segment.AllK = -1 表示不按 K 过滤，返回全量 posting list)
//    initCursors(k) 整体删除

// retrieveAll: K 从 EntryID 动态读
func (e *BooleanEngine) retrieveAll(ctx *RetrieveContext, fCursors *FieldCursors) {
    fCursors.Sort()
    for fCursors.Len() > 0 {
        eid := fCursors.Peek()
        conjID := eid.GetConjID()
        stepK := conjID.Size()
        needMatchCnt := max(1, stepK)

        if needMatchCnt > fCursors.Len() { break }

        endEID := fCursors.PeekAt(needMatchCnt-1)

        nextID := NewEntryID(endEID.GetConjID(), false)
        if endEID.GetConjID() == conjID {
            nextID = NewEntryID(endEID.GetConjID(), true) + 1
            if eid.IsInclude() {
                ctx.Collector.Add(conjID.DocID(), conjID)
            } else {
                fCursors.ShortCircuitAfter(needMatchCnt, nextID)
            }
        }

        fCursors.AdvanceFirst(needMatchCnt, nextID)
        fCursors.CompactLast()
    }
}
```

### 3.4 Layer 4: `builder`

**builder/doc_exporter.go:** `postingSink` 接口保留 K 参数（调用方继续传，builder 层兼容）。

```go
// 接口不变，但实现 side 忽略 K 维度
type postingSink interface {
    AddPosting(k int, field string, term string, entries []core.EntryID) error
    AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}
```

### 3.5 Layer 5: `parser`

无需改动。Encoder 不依赖 K。

## 4. Segment Format: v3 → v4

### v3 (current)

```
MetaBlock:
  BlockIndex:
    "k1_age_dict":     {K:1, Field:"age", Kind:"dict",     Offset, Size}
    "k1_age_postings": {K:1, Field:"age", Kind:"postings", Offset, Size}
    "k3_age_dict":     {K:3, Field:"age", Kind:"dict",     Offset, Size}
```

### v4 (compact)

```
MetaBlock:
  Version: 4
  BlockIndex:
    "age_dict":     {Field:"age", Kind:"dict",     Offset, Size}
    "age_postings": {Field:"age", Kind:"postings", Offset, Size}
    "age_ac":       {Field:"age", Kind:"ac",       Offset, Size}    // per field
    "age_range":    {Field:"age", Kind:"range",    Offset, Size}
```

**所有 `K` 字段从 BlockDef 中删除。** `BlockDef.K` 字段保留零值但不再参与 blockKey 或任何逻辑（纯元数据残留，写 0）。

Version number: `3` → `4`。`NewSegmentReader` 硬编码拒绝非 v4 段。

## 5. API Signature Changes Summary

| Package | Old API | New API | Change |
|---------|---------|---------|--------|
| `core` | `(no KStartEntryID)` | `func KStartEntryID(k int) EntryID` | **新增** |
| `core` | `(no ReachEnd)` | `func (f *FieldCursor) ReachEnd() bool` | **新增** |
| `core` | `(no CompactLast)` | `func (fcs *FieldCursors) CompactLast()` | **新增** |
| `core` | `(no Count/EntryIndex/SubView)` | `FlatPostingList.Count()/EntryIndex()/SubView()` | **新增** |
| `segment` | `func makeBlockKey(k int, f uint16) blockKey` | `func makeBlockKey(f uint16) blockKey` | **K 移除** |
| `segment` | `func blockName(k, field string) string` | `func blockName(field string) string` | **K 移除** |
| `segment` | `func dictBlockName(k, field) string` | `func dictBlockName(field) string` | **K 移除** |
| `segment` | `func (sr) lookupBlock(k int, field BEField)` | `func (sr) lookupBlock(field BEField)` | **K 移除** |
| `segment` | `func GetPostingsByTerm(k int, ...)` | 保留签名；内部 K-range sub-view | 签名不变 |
| `engine` | `func (e) RetrieveWithCollector(...)` | 内部逻辑切换 Compact | 签名不变 |
| `engine` | `func (e) initCursors(k int, ...)` | 删除；新增 `initCursorsOnce(...)` | **重构** |
| `engine` | `func (e) retrieveK(ctx, cursors, k)` | 删除；新增 `retrieveAll(ctx, cursors)` | **重构** |
| `builder` | `type postingSink interface` | 保留 K 参数；实现忽略 | 接口不变 |

## 6. Implementation Phases

### Phase 1: `core` — K-Boundary 工具 + 接口扩展

**目标**: 为上层提供 K-range 定位和游标管理能力，自身独立可编译、可测试。

**改动文件**:
- `core/id_types.go` — 新增 `KStartEntryID(k int) EntryID`
- `core/definitions.go` — PostingIterator 接口新增 `ReachEnd() bool`
- `core/cursor.go` — FieldCursor 新增 `ReachEnd()`；FieldCursors 新增 `CompactLast()`；`SliceIterator` 实现 `ReachEnd()`（`idx >= len(EIDs)`）

Segments 侧：
- `segment/posting_list.go` — `flatPostingCursor` 新增 `ReachEnd()`（`idx >= pl.count`）

**验证**:
- `go test ./core/` 全部通过
- 新增单元测试: `TestKStartEntryID`, `TestFieldCursor_ReachEnd`, `TestFieldCursors_CompactLast`

**不依赖其他 Phase，可独立验证。**

---

### Phase 2: `segment` — 存储层消除 K

**目标**: segment 格式重写为 v4，blockKey/blockName 去掉 K，builder 合并 fieldData。

**改动文件**:
- `segment/format.go` — `blockKey` 改为 `uint16`；`makeBlockKey` 去 K；`BlockDef.K` 标记为 deprecated；新增 `SegmentVersionV4 = 4`
- `segment/block_name.go` — 所有函数去掉 K 参数
- `segment/posting_list.go` — 新增 `Count()`, `EntryIndex(uint32)`, `SubView()`, `SubViewByK()`；更新 `flatPostingCursor` 实现 `ReachEnd()`
- `segment/segment_reader.go` — `lookupBlock` 去 K；`GetPostingsByTerm`/`GetRangePostings`/`MultiPatternSearch` 做 K 子区间二分；版本号 `SegmentVersionV3` → `SegmentVersionV4` (值=4)
- `segment/segment_builder_mem.go` — `fieldData`/`rangeData` 去 K 维度；`Write()` 合并级分组
- `segment/segment_builder_external.go` — `postingRecord` 去 K 字段；`lessPostingRecord`/`writeMergedBlocks` 调整为 `(field, term)` 两级分组

**验证**:
- `go test ./segment/` 全部通过
- 更新 segment 读写 roundtrip 测试（新格式验证）
- 更新 corrupt/truncated 输入测试
- 更新 ExternalBuilder byte-identity 测试

**依赖**: Phase 1（需 `KStartEntryID` 做二元二分定位，需 `ReachEnd()` 接口）

**⚠️ 注意**: Phase 2 后 engine 层编译会失败（`lookupBlock(k, field)` → `lookupBlock(field)`），但 segment 层自身必须全部通过。

---

### Phase 3: `engine` — Compact 检索

**目标**: 切换到 `initCursorsOnce` + `retrieveAll`，移除外层 K 循环。

**改动文件**:
- `engine/searcher.go` — 删除 `initCursors(k int)`；新增 `initCursorsOnce()`（调 `seg.GetPostingsByTerm(-1, ...)`）；删除 `retrieveK()`；新增 `retrieveAll()`；`RetrieveWithCollector` 内部切换

**验证**:
- `go test ./engine/` 全部通过 + race detector
- 更新现有 engine 测试适配新检索路径
- 运行 benchmark (`BenchmarkRetrieveHighK`, `BenchmarkCompositeEngine`)

**⚠️ 注意**: SegmentReader 的 `GetPostingsByTerm(-1, ...)` 返回全量 posting cursor，binary search SkipTo 保证大量 entries 下的检索效率。

---

### Phase 4: `builder` — 调用方适配

**目标**: 适配 `postingSink` 接口实现侧的 K 参数（传但不用），清理构建侧 K 引用。

**改动文件**:
- `builder/doc_exporter.go` — 无签名变化，但确认 `exportDocToSink` 等仍正常工作
- `builder/artifact.go` — 确认 `buildSegmentFromDocsWithCodec` 新格式正确生成

**验证**:
- `go test ./builder/` 全部通过（含 shadow test）

---

### Phase 5: 交叉验证 — Shadow Test

**目标**: 用 oracle 对标，确保 compact 检索结果与原实现完全一致。

**方法**:
- `builder/shadow_test.go` 中已有对比逻辑（engine vs oracle）
- 确认 compact engine 输出与 oracle 100% 一致
- 如存在已有的 baseline 数据，做回归比对

**验证**:
- `go test -v -run Shadow ./builder/` 零差异

---

### Phase 6: 清理与文档

**目标**: 删除废弃代码，更新文档和注释。

**清理项**:
- 删除 `engine/searcher.go` 中废弃的 `initCursors(k int)` 和 `retrieveK()`
- 删除所有代码中残留的 K 分组相关注释
- 更新 `AGENTS.md`、`README.md` 中架构描述
- 更新 `be_indexer.go` root API（如有 K 相关的废导出）
- 删除 `BlockDef.K` 字段（彻底移除，不是标记 deprecated）
- 更新 SegmentVersion → 4 的引用

---

## 7. Verification Checklist

每个 Phase 结束时执行：

- [ ] `go build ./...` 全量编译通过
- [ ] `go test -race ./...` 全量 race 检测通过
- [ ] 目标 package 的测试覆盖率不下降
- [ ] shadow test 零差异
- [ ] benchmark 性能不退化

全部 Phase 完成后增加：

- [ ] `go vet ./...` 无警告
- [ ] 随机 query 100,000 次对比 Oracle，零错误
- [ ] 百万文档构建，ExternalBuilder 不 OOM
- [ ] 多 Segment 场景验证

## 8. Risks & Mitigations

| Risk | Mitigation |
|------|-----------|
| K boundary 二分查找额外开销影响检索延迟 | Benchmark 验证；O(log N) 二分相比 O(log N) dict 查找可忽略 |
| 合并后 posting list 变大→更多 SkipTo 调用 | SkipTo 使用二分搜索，O(log N)；实测验证 |
| needMatchCnt 动态计算引入边缘条件 | 保留 `> len(fCursors)` 判断 + 提前退出 |
| Range/AC 的 K-range 子区间逻辑错误 | Phase 5 shadow test 覆盖 range 和 AC 场景 |
| ExternalBuilder 合并分组后合并排序正确性 | byte-identity 测试 + 多文档构建 diff 对比 |

## 9. Rollback Path

每个 Phase 独立提交。若某 Phase 出现问题，回退到上一个 Phase commit：

```
Phase 1: commit A
Phase 2: commit B
Phase 3: commit C   ← 如果这里出问题，回退到 B
...
```

Segment v3 和 v4 不兼容，因此一旦 Phase 2 提交，`segment` 包不可回退到 v3 格式。但 Phase 2 自身包含 v4 的完整读写 roundtrip，不存在"写了 v4 读不了"的问题——因为写和读是同步变更的。
