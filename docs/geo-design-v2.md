# Geo Radius 定向检索方案（终版）

## 1. 问题

支持「位置在 (lat,lng) 半径 R 米内」的精确查询，作为布尔表达式的一个谓词参与 K-Groups 求交。

---

## 2. 核心策略

```
geohash 粗筛 → Haversine 精排
```

- **geohash**：将 2D 坐标编码为 1D 字符串，前缀越接近 → 空间越近
- **粗筛**：查询时用 geohash 快速过滤 99.9% 的无关文档
- **精排**：Haversine 精确验证候选文档到查询点的距离 ≤ 文档半径

---

## 3. 为什么不用 FlatDict

FlatDict 是 `term → PostingRef` 一对一映射。geo 场景需要 per-entry 元数据：

```
Doc1: 中心(39.9,  116.4),  radius=5000m → geohash "wx4g0e2j"
Doc2: 中心(39.91, 116.41), radius=3000m → geohash "wx4g0e2j"  (同一 cell!)
```

两个文档相同 geohash 但不同参数，FlatDict 无法存储两套元数据。

**方案**：排序数组 `geoEntry{geohash, lat, lng, radius, PostingRef}`，每个 entry 一个文档。

---

## 4. 数据结构

### 4.1 geoEntry

```go
type geoEntry struct {
    geohash string             // 精度 8 的 geohash, base32 编码, 8 字节
    lat     float64            // 文档中心纬度
    lng     float64            // 文档中心经度
    radius  int32              // 文档半径 (米)
    ref     segment.PostingRef // 倒排 offset + count
}
```

### 4.2 容器二进制布局（mmap）

```
Geo Container Block:
┌─────────────────────────────────────────────────────────────┐
│ [magic    "GEO2\0"]                                        │  5B
│ [count    uint32]                                          │  4B
│ [reserved uint32]                                          │  4B
├─────────────────────────────────────────────────────────────┤
│ GeoEntry Array (按 geohash 字典序排序)                      │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ [geohashLen uint16]      2B    (固定 8, 预留变长)       │ │
│ │ [geohash    [8]byte]     8B    base32 编码              │ │
│ │ [lat        float64]     8B                             │ │
│ │ [lng        float64]     8B                             │ │
│ │ [radius     int32]       4B                             │ │
│ │ [plOff      uint64]      8B    倒排 offset              │ │
│ │ [plCount    uint32]      4B    倒排 entry 数            │ │
│ │ ─────────────────────────────────────────────────────── │ │
│ │ per entry: 42B                                         │ │
│ └─────────────────────────────────────────────────────────┘ │
├─────────────────────────────────────────────────────────────┤
│ Posting Block (8B 对齐, 同 FlatPostingList 格式)            │
│ ┌─────────────────────────────────────────────────────────┐ │
│ │ [count uint32][pad uint32][EntryID × count]             │ │
│ └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

### 4.3 可选优化：二级前缀索引

```
┌─────────────────────────────────────────────────────────────┐
│ [1026]uint32  prefix_index   ← 前 2 字符 × base32 = 1024   │
│   prefix_index["wx"] = 53210   (entries 中第一个 "wx" 的索引)│
│   prefix_index["wz"] = 54000   (entries 中第一个 "wz" 的索引)│
├─────────────────────────────────────────────────────────────┤
│ GeoEntry Array (同上)                                        │
└─────────────────────────────────────────────────────────────┘

首期可选，O(1) 跳到桶内，桶内二分 ~log(10K) ≈ 14 次比较。
```

---

## 5. 构建流程

### 5.1 流程图

```
Document: Doc{ID:1, "location": GeoParam{39.9, 116.4, 5000}}
                    │
                    ▼
┌──────────────────────────────────────────────────────────┐
│ Phase 1: Encoder.Build()                                 │
│                                                          │
│  prec = 8  (固定精度, cell ≈ 38m × 19m)                  │
│  gh   = encodeGeohash(39.9, 116.4, 8) → "wx4g0e2j"     │
│                                                          │
│  return EncodedPosting{                                  │
│      Kind:  "geo",                                       │
│      Term:  "wx4g0e2j:39.9:116.4:5000",  ← 嵌入坐标     │
│      Value: GeoParam{39.9, 116.4, 5000},                 │
│  }                                                       │
└──────────────────────────┬───────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────┐
│ Phase 2: sink.AddPosting(k, field, posting, []EID)       │
│                                                          │
│  term  = "wx4g0e2j:39.9:116.4:5000"  (unique per doc)   │
│  eid   = core.NewEntryID(conjID, true)                   │
│                                                          │
│  fd.Postings[term] = append(..., eid)                    │
│  fd.Meta[term]    = GeoParam{39.9, 116.4, 5000}          │
│                                                          │
│  注: term 嵌入坐标后每个文档 term 唯一，Meta 不会被覆盖     │
└──────────────────────────┬───────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────┐
│ Phase 3: ContainerBuilder.Write()                        │
│                                                          │
│  1. 收集所有 (geohash, lat, lng, radius, PostingRef)    │
│  2. 按 geohash 排序 → geoEntry[]                         │
│  3. 序列化 geoEntry[] + posting blocks → 二进制 block    │
│  4. 写入 segment                                        │
└──────────────────────────────────────────────────────────┘
```

### 5.2 为什么 term 嵌入坐标

```
term = "wx4g0e2j:39.9:116.4:5000"
       ──────── ──────────────
       geohash   lat:lng:radius
```

- 同 cell 的不同文档产生不同 term → `fd.Meta` 不被覆盖
- ContainerBuilder 解析 term 拿回完整元数据
- InMemorySegmentBuilder 按 term 去重（同 cell 同参数文档合并 postings，正确行为）

---

## 6. 查询流程

### 6.1 流程图

```
用户查询: Assignments{"location": GeoQuery{39.901, 116.401}}
                    │
                    ▼
┌───────────────────────────────────────────────────────────┐
│ Phase 1: Encoder.Query()  —  9-cell 生成                 │
│                                                           │
│  queryPrec = 5  (cell ≈ 4.9km, 9-cell 覆盖 ≈ 15km)       │
│  center    = encodeGeohash(39.901, 116.401, 5) → "wx4g0" │
│  neighbors = geoNeighbors("wx4g0")                        │
│            = ["wx4g2","wx4g1","wx4fb"...] (8 个)          │
│  cells     = ["wx4g0","wx4g2","wx4g1",...,"wx4fb"]       │
│                                                           │
│  注: query 精度 5 覆盖 15km 区域，能匹配 radius ≤ 7.5km  │
│      的文档。超大 radius 可降为精度 4 (156km cell)        │
└──────────────────────────┬────────────────────────────────┘
                           │
                           ▼
┌───────────────────────────────────────────────────────────┐
│ Phase 2: Reader.Retrieve()  —  前缀范围二分              │
│                                                           │
│  for each cell in 9 cells:                                │
│                                                           │
│    // 二分查找范围: [cell, nextCell)                       │
│    // 例如 cell="wx4g0", nextCell="wx4g1"                  │
│    start = sort.Search(N, func(i) {                       │
│        return entries[i].geohash >= cell                  │
│    })                                                     │
│    end   = sort.Search(N, func(i) {                       │
│        return entries[i].geohash >= nextCell              │
│    })                                                     │
│                                                           │
│    // 遍历 [start, end) 做 Haversine 过滤                │
│    for i := start; i < end; i++ {                         │
│        dist = haversine(qLat, qLng,                      │
│                         entries[i].lat, entries[i].lng)   │
│        if dist <= entries[i].radius {                     │
│            result = append(result,                       │
│                NewPostingCursor(entries[i].ref))          │
│        }                                                 │
│    }                                                     │
│                                                           │
│  return []PostingIterator  (候选 cursors)                 │
└──────────────────────────┬────────────────────────────────┘
                           │
                           ▼
┌───────────────────────────────────────────────────────────┐
│ Phase 3: K-Groups mergeCursors — 不变                     │
│                                                           │
│  geo 返回的 PostingIterator 与其他字段的 cursor 一起      │
│  参与 K-Groups 多路归并。EntryID 的 K 编码在高位，       │
│  排序后自然按 K 分组，引擎完成求交和收集 DocID。          │
└───────────────────────────────────────────────────────────┘
```

### 6.2 前缀范围查找原理

```
build entries (精度 8, 字典序):
  entries[53200] = {"wx4fzzyz", 40.12, 116.01, 1000, ref}
  entries[53201] = {"wx4g09ab", 39.90, 116.41, 5000, ref}  ← start
  entries[53202] = {"wx4g0e2j", 39.91, 116.40, 3000, ref}
  entries[53203] = {"wx4g0e2j", 39.91, 116.41, 5000, ref}
  entries[53204] = {"wx4g0e3k", 39.92, 116.42, 2000, ref}  ← end-1
  entries[53205] = {"wx4g1000", 39.93, 116.43, 1000, ref}  ← end

query cell = "wx4g0" (精度 5)
nextCell   = "wx4g1" (+"\x01" 到下一个 5-char prefix)

sort.Search(≥ "wx4g0")  → 53201
sort.Search(≥ "wx4g1")  → 53205

遍历 entries[53201:53205]:
  "wx4g09ab" → 所有 geohash 以 "wx4g0" 开头 ← 都命中
  "wx4g0e2j" →                                        → Haversine 验证
  "wx4g0e3k" →
```

### 6.3 nextCell 计算

```go
func nextGeohash(s string) string {
    b := []byte(s)
    b[len(b)-1]++  // 最后一位 +1 (base32 循环到下一个有效字符)
    return string(b)
}
```

---

## 7. 复杂度

| 阶段 | 复杂度 | 说明 |
|------|--------|------|
| 9-cell 生成 | O(1) | 固定 9 个 geohash 编码 |
| 二分查找 | O(9 × log N) | 每 cell 一次范围二分 |
| Haversine | O(K) | K 为 9-cell 内候选文档数 |
| K-Groups 归并 | O(E × log F) | 不变 |

| N=10M | 二分比较 | Haversine 调用 | 预计耗时 |
|-------|---------|--------------|---------|
| radius 1km (K≈100) | 216 | 100 | ~2μs |
| radius 100km (K≈300K) | 216 | 300K | ~3ms |

---

## 8. 接口变更

### 8.1 postingSink

```go
// 当前
type postingSink interface {
    AddPosting(k int, field string, term string, entries []core.EntryID) error
    AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}

// 改为
type postingSink interface {
    AddPosting(k int, field string, posting parser.EncodedPosting, entries []core.EntryID) error
    AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error
}
```

提交完整的 `EncodedPosting`（含 `Term` 和 `Value`），无需后续再改接口。

### 8.2 FieldData

```go
type FieldData struct {
    Dict     map[string]PostingRef
    Postings map[string][]core.EntryID
    Meta     map[string]any  // 新增
}
```

`Write()` 时检测容器是否实现 `ContainerMetaBuilder`，遍历 terms 调用 `AddMeta(term, ref, meta)`。

### 8.3 调用点影响

| 文件 | 变更 |
|------|------|
| `builder/doc_exporter.go` | `AddPosting(k, field, posting, []EID)` |
| `segment/segment_builder_mem.go` | 签名 + `FieldData.Meta` + container meta 传递 |
| `segment/external_builder.go` | 签名同步 |
| `segment/segment_test.go` | 术语封装 `EncodedPosting{Term: term}` |
| `segment/*_test.go` | 同上 |

---

## 9. 实现文件

| 文件 | 类型 | 说明 |
|------|------|------|
| `builder/doc_exporter.go` | 修改 | `AddPosting` 签名 |
| `segment/segment_builder_mem.go` | 修改 | 签名 + `FieldData.Meta` + container meta 路由 |
| `segment/external_builder.go` | 修改 | 签名同步 |
| `container/geo/encoder.go` | 重写 | 固定精度 build + 9-cell query + `GeoQueryCells` |
| `container/geo/geo.go` | 重写 | 排序数组 Reader + Builder + Haversine |
| `container/geo/geo_test.go` | 扩展 | 完整正确性验证 |
| `oracle/evaluator.go` | 扩展 | geo radius 暴力评估对照 |
| `segment/*_test.go` | 修改 | AddPosting 调用适配 |

---

## 10. 正确性验证

### 10.1 Oracle 对照

```go
// oracle/evaluator.go
func (e *Evaluator) checkGeoRadius(doc *Document, assigns Assignments) bool {
    for _, conj := range doc.Cons {
        for field, exprs := range conj.Predicates {
            if _, isGeo := exprs[0].Value.(GeoParam); !isGeo {
                continue
            }
            query, ok := assigns[field].(GeoQuery)
            if !ok { continue }
            param := exprs[0].Value.(GeoParam)
            dist := haversine(query.Lat, query.Lng, param.Lat, param.Lng)
            if !exprs[0].Incl {
                if dist <= param.Radius { return false }
            } else {
                if dist > param.Radius { return false }
            }
        }
    }
    return true
}
```

### 10.2 边界测试

| 场景 | 验证点 |
|------|--------|
| 查询点在半径边界上 (dist == radius) | 包含 |
| 查询点在半径外 1m | 排除 |
| 文档在 geohash cell 边界上 | 9-cell 覆盖 |
| 两个文档同 cell 不同中心/半径 | 各自正确判断 |
| 赤道/本初子午线附近 | geohash 无畸变区 |
| radius=0 | 仅精确点 |
| radius=10000km | 大范围覆盖 |
| Exclude 谓词 (Incl:false) | mergeCursors 正确处理 |

---

## 11. 存储估算

| 规模 | 每 entry | 总计 |
|------|---------|------|
| 42B/entry 容器 | 8B geohash + 20B 坐标 + 14B PostingRef | |
| 8B + padding/EntryID 倒排 | ≈16B/entryID | |
| 10M entries | ~420MB + ~160MB | **~600MB** |
