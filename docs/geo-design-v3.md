# Geo Radius 定向检索方案（终版, proximityhash）

## 1. 核心思路

> 用 `proximityhash` 把 (lat,lng,r) 展开为固定数量 geohash cell，作为普通 term 存入 FlatDict。查询时把 (lat,lng) 展开为多精度前缀，直接走 FlatDict 精确查 term。

**不需要自定义 container。不需要新接口。不需要 Haversine 后过滤。**

---

## 2. 为什么可以不走自定义 container

build 和 query 两侧都用 fixed set of strings，两侧保证交集非空 → 候选。本质是：

```
Build:  Circle → {geohash codes} → 存入 Term → FlatDict → PostingList
Query:  Point  → {geohash prefixes} → FlatDict.Find → 命中 → 候选
```

```go
// 文档: 半径 5km @ (39.9, 116.4)
proximityhash.CreateGeohash(39.9, 116.4, 5000, precision=7)
// → ["wx4g0e", "wx4g0g", "wx4g0u", ...]  覆盖圆的 cell 集合

// 查询: 用户 @ (39.901, 116.401)
encodeGeohash(39.901, 116.401, 8) → "wx4g0e2j"
prefixes: ["wx4","wx4g","wx4g0","wx4g0e","wx4g0e2","wx4g0e2j"]
                 ↑
         FlatDict.Find("wx4g0e") → 命中！
```

---

## 3. 架构

```
Document: GeoParam{lat,lng,radius}
                │
                ▼
┌───────────────────────────────────────────────────────────┐
│ Encoder.Build()                                           │
│                                                           │
│  prec = precisionForRadius(radius)                        │
│  codes = proximityhash.CreateGeohash(lat, lng, r, prec)   │
│  codes = proximityhash.CompressGeoHash(codes, ...)        │
│                                                           │
│  → []EncodedPosting{Kind:"term", Term: code}             │
│    (3-16 个 posting，按 cell 数)                          │
└───────────────────────────┬───────────────────────────────┘
                            │
                            ▼
┌───────────────────────────────────────────────────────────┐
│ doc_exporter.go                                           │
│                                                           │
│  Kind="term" → 走标准路径                                  │
│  sink.AddPosting(k, field, code, []EID{eid})              │
│  → FlatDict[code] → PostingRef → FlatPostingList         │
│                                                           │
│  完全复用现有词典+倒排体系，不创建自定义 container           │
└───────────────────────────┬───────────────────────────────┘
                            │
                            ▼
                    ┌──────────────┐
                    │  FlatDict    │  (geohash → PostingRef)
                    │  (已存在)    │  O(log N) 二分查找
                    └──────┬───────┘
                           │
                    ┌───────▼──────┐
                    │ PostingList  │  (EntryID[])
                    │ (已存在)     │  O(1) mmap
                    └──────────────┘
```

---

## 4. 查询

```
Query: GeoQuery{Lat, Lng}
                │
                ▼
┌───────────────────────────────────────────────────────────┐
│ Encoder.Query()                                           │
│                                                           │
│  gh = encodeGeohash(lat, lng, maxPrecision)  // 精度 8    │
│  for i := minPrecision; i <= maxPrecision; i++ {          │
│      queries = append(queries, EncodedQuery{              │
│          Kind: "term",                                    │
│          Term: gh[:i],  // "wx4","wx4g","wx4g0",...       │
│      })                                                   │
│  }                                                        │
│  → []EncodedQuery{Kind:"term", Term: prefix}              │
└───────────────────────────┬───────────────────────────────┘
                            │
                            ▼
┌───────────────────────────────────────────────────────────┐
│ engine.initCursors()                                      │
│                                                           │
│  Kind="term" → seg.GetPostingsByTerm(field, prefix)       │
│              → FlatDict.Find(prefix)                      │
│              → O(log N) × (maxPrec - minPrec + 1)         │
│              → ~4 次二分查找                               │
└───────────────────────────┬───────────────────────────────┘
                            │
                            ▼
┌───────────────────────────────────────────────────────────┐
│ mergeCursors (不变)                                       │
│                                                           │
│  返回的 PostingIterator 与其他字段一起参与 K-Groups 归并   │
└───────────────────────────────────────────────────────────┘
```

---

## 5. 精度选择

### 5.1 Build 精度: precisionForRadius

```go
func precisionForRadius(radiusMeters int) int {
    // 选择 cell 对角线一半 < radius 的精度
    // 确保过覆盖误差 < radius
    target := radiusMeters
    for _, pt := range precisionTable {
        if pt.meters <= target {
            return pt.precision
        }
    }
    return 8
}

var precisionTable = []struct {
    precision int  // geohash 字符数
    meters    int  // cell 近似宽 (经度方向, 赤道)
}{
    {6, 1200},   // ~1.2km cell → 对角线半 ≈ 850m
    {7, 152},    // ~152m cell  → 对角线半 ≈ 108m
    {8, 38},     // ~38m cell   → 对角线半 ≈ 27m
}
```

| radius | 精度 | cell | 过覆盖误差 | 覆盖 circle 的 cell 数 |
|--------|------|------|-----------|----------------------|
| 50km | 5 | 4.9km | ~3.5km | 20-30 |
| 5km | 6 | 1.2km | ~850m | 9-16 |
| 1km | 7 | 152m | ~108m | 12-20 |
| 100m | 8 | 38m | ~27m | 9-16 |

### 5.2 Query 精度范围

```go
minPrecision = 3  // 最粗: 156km cell, 覆盖任何合理半径
maxPrecision = 8  // 最细: 38m cell
```

查询产生 6 个前缀 (3→8)，每个 FlatDict.Find 一次。

### 5.3 压缩

```go
codes = proximityhash.CompressGeoHash(codes, minPrecision, prec)
```

将同父的相邻 cells 合并为父 cell 前缀。例如 4 个精度 7 的 cells 合并为 1 个精度 6 的 prefix。减少存储 term 数 2-4×。

---

## 6. API 影响

**零接口变更。**

| 组件 | 变更 |
|------|------|
| `postingSink` | 不变 (`AddPosting(k, field, term, entries)`) |
| `InMemorySegmentBuilder` | 不变 |
| `ExternalBuilder` | 不变 |
| `ContainerBuilder/Reader` | 不注册（geo 不走自定义容器） |
| `container/geo/geo.go` | **删除**（不需要了） |

---

## 7. Engine 查询路径

```go
// engine/searcher.go:214-220 — 不变
switch q.Kind {
case parser.QueryKindTerm:
    it, err := seg.GetPostingsByTerm(field, q.Term) // 直接走这里
}
```

geo 的 Query 返回 `QueryKindTerm`，engine 走已有标准路径，**无 switch 分支增加**。

---

## 8. 复杂度

| 阶段 | 当前 (旧 geo) | 改进后 | 说明 |
|------|-------------|--------|------|
| Build | 1 term/doc | 3-16 term/doc | 取决于 radius 和精度 |
| Query | O(N) 线性遍历所有 point | O(6 × log N) ≈ 144 次比较 | N=10M |
| 存储 | 1 posting/doc | 3-16 posting/doc | 紧凑 (每 posting 一个 EntryID) |

---

## 9. 正确性

### 9.1 精度保证

过覆盖误差 = cell 对角线一半。选择精度使该误差 < radius 的 10-20%。

**对广告定向：**
- 1km radius @ 精度 7 → cell ~152m → 误差 ~108m。查询点在覆盖区内最多 1108m 处也会匹配，误差 ~11%。广告场景完全可接受。
- 100m radius @ 精度 8 → cell ~38m → 误差 ~27m。误差 ~27%。

### 9.2 Covering 保证

`proximityhash` 保证生成的 cell 集合**覆盖整个圆**。任何一个在圆内的查询点，其 geohash 至少与一个 covering cell 共享前缀。

### 9.3 边界测试

| 场景 | 行为 |
|------|------|
| 查询点在圆心 | 中心 cell 精确匹配 |
| 查询点在圆边界上 | 边界 cell 覆盖 |
| 查询点在圆外 1m | 若在 covering cell 内则误匹配（精度控制的误差范围内） |
| 两个文档同 cell 不同参数 | 各自独立 posting，按参数匹配 |

---

## 10. 实现文件

**全部变更 ≤ 2 个文件：**

| 文件 | 变更 | 行数估计 |
|------|------|---------|
| `container/geo/encoder.go` | 重写：proximityhash build + 多精度 query | ~80 行 |
| `container/geo/geo_test.go` | 重写：完整正确性测试 | ~100 行 |

**可删除：**

| 文件 | 说明 |
|------|------|
| `container/geo/geo.go` | 自定义 container (Builder + Reader)，不再需要 |
| `container/geo/encoder.go:init` 中的 `RegisterContainer("geo", ...)` | 不再注册 container |

保留 `RegisterPredicateEncoder("proximitygeo", ...)` 用于 encoder。

---

## 11. 实现骨架

```go
// container/geo/encoder.go

import (
    "github.com/echoface/proximityhash"
    "github.com/mmcloughlin/geohash"
)

func (e Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
    param := expr.Value.(GeoParam)
    prec := precisionForRadius(param.Radius)

    codes := proximityhash.CreateGeohash(
        param.Lat, param.Lng, float64(param.Radius), uint(prec),
    )
    codes = proximityhash.CompressGeoHash(codes, 3, prec)

    out := make([]parser.EncodedPosting, len(codes))
    for i, c := range codes {
        out[i] = parser.EncodedPosting{Kind: parser.PostingKindTerm, Term: c}
    }
    return out, nil
}

func (e Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
    q := value.(GeoQuery)
    gh := geohash.Encode(q.Lat, q.Lng)

    const minPrec, maxPrec = 3, 8
    out := make([]parser.EncodedQuery, 0, maxPrec-minPrec+1)
    for p := minPrec; p <= maxPrec && p <= len(gh); p++ {
        out = append(out, parser.EncodedQuery{
            Kind: parser.QueryKindTerm,
            Term: gh[:p],
        })
    }
    return out, nil
}

// init 只注册 encoder，不注册 container
func init() {
    parser.RegisterPredicateEncoder("proximitygeo", func(core.FieldMeta) (parser.PredicateEncoder, error) {
        return Encoder{}, nil
    })
}
```

---

## 12. 为什么这是最终方案

1. **零风险** — 不改 segment 层、不新增 container、不扩接口
2. **复用** — 100% 走 FlatDict + FlatPostingList 已有基础设施
3. **简单** — 核心逻辑就是 `proximityhash.CreateGeohash` + `geohash.Encode` + 取前缀
4. **正确** — `proximityhash` 保证 covering 完整，精度控制保证误差可量化
5. **可升级** — 将来如需精确 Haversine，再加 container 即可，不影响当前
