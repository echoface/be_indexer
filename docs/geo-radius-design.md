# Geo Radius Query 设计方案

## 1. 背景与目标

### 1.1 当前问题

`container/geo/` 现有实现存在根本性缺陷：

| 问题 | 影响 |
|------|------|
| 单 geohash 编码，无 9-cell 邻居覆盖 | 跨 cell 边界的点漏匹配 |
| `GeoParam{lat,lng,radius}` 元数据在序列化时丢失 | 查询时无法做精确距离校验 |
| 无 Haversine 后过滤 | geohash 前缀匹配 ≠ 距离检查 |

### 1.2 目标

- 支持 "位置在 (lat,lng) 半径 R 米内" 的精确查询
- 保持 mmap 零拷贝设计哲学
- 向后兼容旧格式 segment
- 查询复杂度从 O(N) 降至 O(9 × log N + K)

### 1.3 Self-Review 关键发现

**发现 1：多文档同 geohash 问题**

两个文档可能有相同的 geohash 但不同的中心点/半径：

```
Doc1: center (39.9, 116.4), radius 5000m → geohash "wx4g0" (precision 5)
Doc2: center (39.91, 116.41), radius 5000m → geohash "wx4g0" (same cell!)
```

如果元数据按 geohash 存储（而非按 EntryID），则无法区分这两个文档的不同半径。

**解决方案**：元数据按 EntryID 索引，不按 geohash 索引。使用排序数组存储每个 geo entry 的完整信息。

**发现 2：precisionForRadius 的合并效应**

不同 radius 可能映射到相同 precision：

```
precisionForRadius(4000) → 5 (cell ~4.9km)
precisionForRadius(5000) → 5 (cell ~4.9km)
```

这意味着 radius 4000m 和 5000m 的文档会使用相同的 geohash 精度，但中心点不同会导致不同的 geohash 字符串。这不影响正确性，但说明 geohash 不能作为元数据的唯一键。

**发现 3：API 变更影响范围**

`AddPosting` 接口变更影响 ~20 个调用点（2 个生产代码 + ~18 个测试），需要机械式修改。

---

## 2. 接口变更

### 2.1 `postingSink` 接口统一

**当前：**

```go
type postingSink interface {
    AddPosting(k int, field string, term string, entries []core.EntryID) error
    AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}
```

**改为：**

```go
type postingSink interface {
    AddPosting(k int, field string, posting parser.EncodedPosting, entries []core.EntryID) error
    AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}
```

**变更理由：**

- `posting` 参数传递完整 `EncodedPosting`，包含 `Kind`、`Term`、`Value`（元数据）
- 后续自定义容器扩展无需再改接口
- 内置容器（term/AC/range）忽略 `Value` 字段，无兼容性问题

### 2.2 实现者签名变更

| 实现者 | 当前签名 | 新签名 |
|--------|---------|--------|
| `InMemorySegmentBuilder` | `AddPosting(k, field, term, entries)` | `AddPosting(k, field, posting, entries)` |
| `ExternalBuilder` | `AddPosting(k, field, term, entries)` | `AddPosting(k, field, posting, entries)` |

### 2.3 `FieldData` 扩展

```go
type FieldData struct {
    Dict     map[string]PostingRef
    Postings map[string][]core.EntryID
    Meta     map[string]any  // 新增：term → 容器元数据
}
```

### 2.4 ContainerBuilder 调用路径

```go
// segment_builder_mem.go Write() 中 custom container 路径
case meta.Container != "" && meta.Container != core.IndexNameDefault:
    cb, err := NewContainerBuilder(meta.Container)
    if cm, ok := cb.(ContainerMetaBuilder); ok {
        for _, term := range terms {
            cm.AddMeta(term, fd.Dict[term], fd.Meta[term])
        }
    }
    // ... Build()
```

### 2.5 调用点影响清单

| 文件 | 行号 | 变更 |
|------|------|------|
| `builder/doc_exporter.go` | 189 | `sink.AddPosting(k, field, posting, []EID{eid})` |
| `builder/doc_exporter.go` | 199 | `sink.AddPosting(k, field, posting, []EID{eid})` |
| `segment/segment_test.go` | 29,30,167,202,231,263 | 包装 `EncodedPosting{Term: term}` |
| `segment/segment_builder_external_test.go` | 45,98,142,188,192 | 同上 |
| `segment/posting_list_test.go` | 62,63,64 | 同上 |
| `segment/segment_ac_test.go` | 18,19,20,21 | 同上 |

---

## 3. 构建流程

### 3.1 构建示意图

```
用户定义 Document:
┌──────────────────────────────────────────────────────────────┐
│ Doc{ID:1, Cons:[                                             │
│   Conj{Predicates:{                                          │
│     "location": [ValueExpr{Incl:true, Value:GeoParam{       │
│       Lat:39.9, Lng:116.4, Radius:5000}]}]}]}               │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 1: exportDocToSink                                     │
│                                                              │
│  fieldCodec.Encoder.Build(expr)                              │
│  geo.Encoder.Build(GeoParam{39.9, 116.4, 5000})             │
│    │                                                         │
│    ▼                                                         │
│  prec = precisionForRadius(5000)  → 5  (cell ~4.9km)         │
│  gh = encodeGeohash(39.9, 116.4, 5)  → "wx4g0"             │
│    │                                                         │
│    ▼                                                         │
│  return [EncodedPosting{                                     │
│    Kind: "geo",                                              │
│    Term: "wx4g0",                                            │
│    Value: GeoParam{39.9, 116.4, 5000}  ← 元数据保留         │
│  }]                                                         │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 2: sink.AddPosting(k, field, posting, []EID{eid})      │
│                                                              │
│  posting = EncodedPosting{Kind:"geo", Term:"wx4g0",          │
│            Value:GeoParam{39.9,116.4,5000}}                  │
│                                                              │
│  fd.Postings["wx4g0"] = append(..., eid)                     │
│  fd.Meta["wx4g0"] = GeoParam{39.9,116.4,5000}  ← 元数据存储 │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 3: Write() — 序列化 geo container                      │
│                                                              │
│  构建排序数组 geoEntry[]（按 geohash 排序）：                  │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ geoEntry{                                              │  │
│  │   geohash: "wx4g0",                                    │  │
│  │   lat: 39.9, lng: 116.4, radius: 5000,                │  │
│  │   PostingRef{offset, count}                            │  │
│  │ }                                                      │  │
│  │ geoEntry{                                              │  │
│  │   geohash: "wx4g0",  ← 相同 geohash，不同中心点        │  │
│  │   lat: 39.91, lng: 116.41, radius: 5000,              │  │
│  │   PostingRef{offset, count}                            │  │
│  │ }                                                      │  │
│  │ ...                                                    │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  序列化为 mmap 友好的二进制格式（见 3.2）                     │
└──────────────────────────────────────────────────────────────┘
```

### 3.2 序列化二进制布局

```
Geo Container Block:
┌─────────────────────────────────────────────────────────────┐
│ [magic "GEO2\0"]                                           │  5B
│ [count uint32]                                              │  4B
│ [reserved uint32]                                           │  4B
├─────────────────────────────────────────────────────────────┤
│ GeoEntry Array (sorted by geohash, binary searchable)       │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ [geohashLen uint16]                                     │  2B
│ │ [geohash bytes]                                         │  5-8B
│ │ [lat float64]                                           │  8B
│ │ [lng float64]                                           │  8B
│ │ [radius int32]                                          │  4B
│ │ [postingOffset uint64]                                  │  8B
│ │ [postingCount uint32]                                   │  4B
│ │ ─────────────────────────────────────────────────────── │ │
│ │ per entry: ~37-40B (变长 geohash)                       │ │
│ └─────────────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────────────┤
│ Posting Blocks (EntryID arrays, 8-byte aligned)             │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ [count uint32][pad uint32][EntryID × count]             │ │
│ └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

### 3.3 为什么不用 FlatDict

FlatDict 将 geohash → PostingRef 一对一映射。但 geo 场景需要 per-EntryID 元数据：

```
Doc1: geohash "wx4g0", center (39.9, 116.4), radius 5000m
Doc2: geohash "wx4g0", center (39.91, 116.41), radius 5000m
```

这两个文档有相同 geohash 但不同中心点。FlatDict 只能存一个 PostingRef 和一个 metadata，无法区分。

**解决方案**：使用排序数组，每个 geo entry 独立存储完整信息（geohash + lat + lng + radius + PostingRef）。二分查找定位 geohash 后，遍历所有匹配的 entry 做 Haversine 验证。

### 3.4 排序数组内存布局

```
GeoEntry[] (按 geohash 字典序排序):
┌───────┬──────────┬─────────────┬────────┬────────────┬────────────┐
│ Index │ geohash  │ lat, lng    │ radius │ PostingRef │ 候选标记   │
├───────┼──────────┼─────────────┼────────┼────────────┼────────────┤
│   0   │ "wx3     │ 40.1, 116.0 │  1000  │ {off0, c0} │            │
│   1   │ "wx4g0"  │ 39.9, 116.4 │  5000  │ {off1, c1} │   ← 候选  │
│   2   │ "wx4g0"  │ 39.91,116.41│  5000  │ {off2, c2} │   ← 候选  │
│   3   │ "wx4g0"  │ 39.89,116.39│  3000  │ {off3, c3} │   ← 候选  │
│   4   │ "wx4g1"  │ 40.0, 116.5 │  2000  │ {off4, c4} │            │
│  ...  │   ...    │    ...      │  ...   │    ...     │            │
│ N-1   │ "zzzzz"  │ -30.0,30.0  │  1000  │ {offN, cN} │            │
└───────┴──────────┴─────────────┴────────┴────────────┴────────────┘

查询: GeoQueryCells{Cells: ["wx4g0e2j", ...]}

sort.Search 定位第一个 geohash >= "wx4g0e2j":
  lo = 1, hi = 3 (所有 "wx4g0" entries)

遍历 [1, 3] 区间:
  entry[1]: haversine(query, 39.9, 116.4) = 800m <= 5000m → ✓ 匹配
  entry[2]: haversine(query, 39.91, 116.41) = 1200m <= 5000m → ✓ 匹配
  entry[3]: haversine(query, 39.89, 116.39) = 1500m <= 3000m → ✓ 匹配
```

### 3.5 sort.Search 二分查找原理

```go
// sort.Search 在已排序数组中查找第一个 >= target 的位置
idx := sort.Search(len(entries), func(i int) bool {
    return entries[i].geohash >= cell
})

// 遍历所有 geohash == cell 的 entries
for idx < len(entries) && entries[idx].geohash == cell {
    // Haversine 验证 + 创建 PostingCursor
    idx++
}
```

**时间复杂度**：
- `sort.Search`: O(log N) — 二分查找
- 遍历匹配 entries: O(K_cell) — 同一 geohash 的 entries 数量
- 9 cells 总计: O(9 × log N + K)

---

## 4. 查询流程

### 4.1 查询示意图

```
用户查询:
┌──────────────────────────────────────────────────────────────┐
│ Engine.Retrieve(Assignments{                                 │
│   "location": GeoQuery{Lat:39.901, Lng:116.401},            │
│ })                                                           │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 1: encodeQueries — O(1)                                │
│                                                              │
│  geo.Encoder.Query(GeoQuery{39.901, 116.401})                │
│    │                                                         │
│    ▼                                                         │
│  prec = 8 (固定高精度, cell ~38m)                             │
│  centerHash = encodeGeohash(39.901, 116.401, 8)              │
│             = "wx4g0e2j"                                     │
│  neighbors = geoNeighbors("wx4g0e2j")                        │
│            = ["wx4g0e2k","wx4g0e2i",...] (8 个)              │
│  cells = ["wx4g0e2j", "wx4g0e2k", ..., "wx4g0e2h"]          │
│    │                                                         │
│    ▼                                                         │
│  return [EncodedQuery{                                       │
│    Kind: "geo",                                              │
│    Value: GeoQueryCells{                                     │
│      Lat: 39.901, Lng: 116.401,                              │
│      Cells: ["wx4g0e2j","wx4g0e2k",...,"wx4g0e2h"]           │
│    }                                                         │
│  }]                                                          │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 2: initCursors — O(S × (9 × log N + K))                │
│                                                              │
│  for each segment:                                           │
│    seg.ContainerQuery(field, "geo", queryValue)              │
│      │                                                       │
│      ▼                                                       │
│  geo.Reader.Retrieve(postingBlock, field, GeoQueryCells)     │
│      │                                                       │
│      ▼                                                       │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ for each cell in 9 cells:                              │  │
│  │   // 二分查找定位第一个 geohash >= cell 的 entry       │  │
│  │   idx = sort.Search(N, func(i) bool {                 │  │
│  │     return entries[i].geohash >= cell                  │  │
│  │   })                                                   │  │
│  │   // 遍历所有 geohash == cell 的 entry                │  │
│  │   for idx < N && entries[idx].geohash == cell:         │  │
│  │     meta = entries[idx]                                │  │
│  │     dist = haversine(qLat,qLng, meta.lat, meta.lng)   │  │
│  │     if dist <= meta.radius:                            │  │
│  │       cursor = NewPostingCursor(meta PostingRef)       │  │
│  │       result.append(cursor)                            │  │
│  │     idx++                                              │  │
│  └────────────────────────────────────────────────────────┘  │
│      │                                                       │
│      ▼                                                       │
│  return []core.PostingIterator  (K 个匹配的 cursor)          │
└──────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌──────────────────────────────────────────────────────────────┐
│ Phase 3: mergeCursors — O(E × log(F))                        │
│                                                              │
│  K-Groups 多路归并：                                          │
│  - 所有 FieldCursor 按 EntryID 排序                          │
│  - Peek 最小 EntryID，检查是否凑齐 K 个                       │
│  - 匹配则收集 DocID，跳过则 SkipTo                            │
│  - 已知复杂度，不变                                           │
└──────────────────────────────────────────────────────────────┘
```

### 4.2 完整数据流

```
                    ┌─────────────────┐
                    │   Assignment    │
                    │ GeoQuery{lat,lng}│
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  Encoder.Query  │
                    │  9-cell geohash │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │  ContainerQuery │
                    │ (per segment)   │
                    └────────┬────────┘
                             │
              ┌──────────────▼──────────────┐
              │     geo.Reader.Retrieve     │
              │                             │
              │  ┌────────────────────────┐ │
              │  │ sort.Search (×9)       │ │  O(9 × log N)
              │  │ 二分查找定位 geohash   │ │
              │  └───────────┬────────────┘ │
              │              │              │
              │  ┌───────────▼────────────┐ │
              │  │ Haversine 过滤 (×K)    │ │  O(K)
              │  │ 精确距离校验           │ │
              │  └───────────┬────────────┘ │
              │              │              │
              │  ┌───────────▼────────────┐ │
              │  │ PostCursor (×K)        │ │
              │  │ 零拷贝 posting list    │ │
              │  └───────────┬────────────┘ │
              └──────────────┼──────────────┘
                             │
                    ┌────────▼────────┐
                    │  mergeCursors   │  O(E × log F)
                    │  K-Groups 归并  │
                    └────────┬────────┘
                             │
                    ┌────────▼────────┐
                    │   DocID 结果集   │
                    └─────────────────┘
```

---

## 5. 时间复杂度分析

### 5.1 各阶段复杂度

| 阶段 | 当前 | 改进后 | 说明 |
|------|------|--------|------|
| Query 编码 | O(F) | O(1) | F = fields，geo 固定 1 个 |
| Container Query | O(N) | O(9 × log N + K) | N = entries, K = candidates |
| K-Groups 归并 | O(E × log F) | O(E × log F) | 不变 |
| **总计** | **O(N + E × log F)** | **O(9 × log N + K + E × log F)** | |

### 5.2 实际性能估算

| 场景 (N=10M entries) | 当前 O(N) | 改进后 O(9×logN+K) | 加速比 |
|--------------|-----------|---------------------|--------|
| radius 1km (K≈100) | 10M 次比较 | 216 + 100 | **~30,000×** |
| radius 10km (K≈10K) | 10M 次比较 | 216 + 10K | **~1,000×** |
| radius 100km (K≈300K) | 10M 次比较 | 216 + 300K | **~30×** |

### 5.3 复杂度公式推导

**当前实现：**
- `Reader.Retrieve()` 遍历所有 N 个 stored entries
- 每次 `prefixMatch()` 是 O(min(len(q), len(s))) ≈ O(1)（geohash 5-8 字节）
- 总计：O(N)

**改进实现：**
- `sort.Search()` 对 N 个 sorted entries 做二分查找：O(log N)
- 每个 cell 一次二分查找，9 cells：O(9 × log N)
- 匹配的 entries 遍历：O(K)（K = 9-cell 内的 entries 数量）
- Haversine 计算：O(1) per candidate
- 总计：O(9 × log N + K)

**log N 估算：**
- N = 1M → log₂(N) ≈ 20
- N = 10M → log₂(N) ≈ 24
- N = 100M → log₂(N) ≈ 27

---

## 6. 空间开销

| 组件 | 当前 | 改进后 | 增量 |
|------|------|--------|------|
| 每个 geo entry | ~20B (geohash + PostingRef) | ~40B (+lat,lng,radius) | +20B/entry |
| 10M entries | ~200MB | ~400MB | +200MB |
| Sorted array overhead | 0 | ~4B (count header) | 可忽略 |

---

## 7. Haversine 实现

```go
const earthRadiusMeters = 6371000.0

func haversineDistance(lat1, lng1, lat2, lng2 float64) float64 {
    dLat := (lat2 - lat1) * math.Pi / 180.0
    dLng := (lng2 - lng1) * math.Pi / 180.0
    a := math.Sin(dLat/2)*math.Sin(dLat/2) +
        math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
            math.Sin(dLng/2)*math.Sin(dLng/2)
    c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
    return earthRadiusMeters * c
}
```

---

## 8. Geohash 9-Cell 邻居算法

```go
func geoNeighbors(hash string) [8]string {
    // 标准 geohash 邻居计算：
    // 1. 计算 hash 的边界框 (latLo, latHi, lngLo, lngHi)
    // 2. 在 8 个方向 (N, S, E, W, NE, NW, SE, SW) 各偏移一个 cell
    // 3. 编码偏移后的坐标为 geohash
    // 复用 github.com/mmcloughlin/geohash 包的 Neighbors() 函数
}
```

---

## 9. 向后兼容

### 9.1 格式检测

```go
// Reader 初始化时检查 magic number
if len(b) >= 5 && string(b[:5]) == "GEO2\0" {
    // 新格式：排序数组 + 完整元数据
    return newReaderV2(b)
}
// 旧格式：fallback 到线性扫描
return newReaderV1(b)
```

### 9.2 迁移策略

- 旧 segment 继续工作（退化为 O(N) 线性扫描）
- 新 build 自动使用新格式
- 无需强制重建，按需灰度迁移

---

## 10. Benchmark 计划

### 10.1 基准测试

```go
// container/geo/geo_bench_test.go

func BenchmarkGeoRetrieval(b *testing.B) {
    // 构建 1M/5M/10M 个 geo entries
    // 查询不同 radius: 100m, 1km, 10km, 100km
    // 对比: 当前线性扫描 vs 新排序数组方案
}

func BenchmarkHaversine(b *testing.B) {
    // 单次 Haversine 调用耗时
}

func BenchmarkSortSearch(b *testing.B) {
    // sort.Search vs 线性扫描 vs Go map lookup
}

func BenchmarkGeoNeighbors(b *testing.B) {
    // 9-cell 邻居生成耗时
}
```

### 10.2 预期结果

| Benchmark | 当前 | 改进后 | 预期 |
|-----------|------|--------|------|
| 10M entries, 1km query | ~50ms | ~2μs | 25,000× |
| 10M entries, 10km query | ~50ms | ~100μs | 500× |
| 10M entries, 100km query | ~50ms | ~3ms | 16× |
| Haversine 单次调用 | N/A | ~20ns | - |
| 9-cell 邻居生成 | N/A | ~100ns | - |

---

## 11. 正确性验证

### 11.1 Shadow Testing (Oracle)

```go
func TestGeoShadowTesting(t *testing.T) {
    // 1. 构建 N 个随机 geo documents (不同 radius, 不同 center)
    // 2. 对每个 document，用 Oracle (evaluator.go) 暴力评估
    //    - 遍历所有 documents
    //    - 对每个 document，计算 query point 到 center 的距离
    //    - 检查 distance <= radius
    // 3. 对比 BooleanEngine + 新 geo container 的结果
    // 4. 验证完全一致 (false negative = 0, false positive = 0)
}
```

### 11.2 边界测试

| 用例 | 预期 | 验证方法 |
|------|------|---------|
| query point 在 radius 边界上 (dist == radius) | 匹配 | 精确 Haversine 计算 |
| query point 在 radius 外 1m | 不匹配 | 精确 Haversine 计算 |
| query point 在 geohash cell 边界上 | 9-cell 覆盖 | 验证 9-cell 包含正确 cells |
| document radius 跨越赤道 | 正确匹配 | 构建赤道附近的 documents |
| document radius 跨越本初子午线 | 正确匹配 | 构建 0° 经线附近的 documents |
| document radius = 0 | 仅精确点匹配 | 构建 radius=0 的 documents |
| document radius = 极大值 (10000km) | 多 cell 覆盖 | 验证大 radius 的正确性 |
| 多个 documents 共享相同 geohash | 各自独立匹配 | 验证 per-entry metadata |

### 11.3 回归测试

- 现有 `TestGeoRoundTrip` 继续通过
- 新增 `TestGeoRadiusCorrectness` 验证精确距离
- 新增 `TestGeoMultiDocSameGeohash` 验证多文档同 geohash 场景

---

## 12. 实现文件清单

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `segment/container.go` | 无变更 | ContainerMetaBuilder 已存在 |
| `builder/doc_exporter.go` | 修改 | `AddPosting` 签名调整 |
| `segment/segment_builder_mem.go` | 修改 | `AddPosting` 签名 + `FieldData.Meta` + container meta 调用 |
| `segment/external_builder.go` | 修改 | 同步 `AddPosting` 签名 |
| `container/geo/encoder.go` | 重写 | 9-cell 生成 + `GeoQueryCells` 类型 |
| `container/geo/geo.go` | 重写 | 排序数组 + 完整元数据 + Haversine 过滤 |
| `container/geo/geo_test.go` | 扩展 | 新增半径边界测试 + shadow testing |
| `oracle/evaluator.go` | 扩展 | 新增 geo radius 评估支持 |
| `segment/segment_test.go` | 修改 | `AddPosting` 调用适配 |
| `segment/segment_builder_external_test.go` | 修改 | 同上 |
| `segment/posting_list_test.go` | 修改 | 同上 |
| `segment/segment_ac_test.go` | 修改 | 同上 |
