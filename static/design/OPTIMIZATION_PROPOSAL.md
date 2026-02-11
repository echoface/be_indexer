# 工业级布尔索引优化技术方案

## 概述
本文档旨在提出一套分阶段的优化方案，将现有的 `be_indexer` 提升至工业级高性能检索引擎标准。优化主要关注存储效率（内存占用、GC压力）和查询性能（CPU缓存命中率、指令集并行化）。

所有优化设计遵循**“接口兼容、渐进升级”**的原则，确保现有业务逻辑（`KGroupsBEIndex`、`Retrieve`流程）无需大规模重构即可享受性能红利。

---

## 阶段一：扁平化内存布局 (Flattened Memory Layout)

### 1. 问题分析
当前 `DefaultEntriesHolder` 使用 `map[Term]Entries` 存储倒排链，其中 `Entries` 为 `[]uint64`。
*   **内存碎片**：大量的小切片（Slice）导致内存碎片化。
*   **GC 压力**：Go GC 需要扫描 Map 中的每个 Bucket 和 Slice Header，随着 Term 数量增加（如数百万个特征），GC 扫描耗时显著。
*   **缓存不友好**：Map 的链式结构和非连续内存导致 CPU Cache Miss 较高。

### 2. 优化目标
*   消除 `map` 结构，使用连续数组存储数据。
*   降低 GC 扫描对象数量至常数级（O(1)）。
*   保持 `EntriesHolder` 接口兼容。

### 3. 技术方案：`FlatEntriesHolder`

设计一个新的 Holder 实现 `FlatEntriesHolder`。

#### 数据结构设计
```go
type FlatEntriesHolder struct {
    // 1. 字典区 (Dictionary)
    // 映射 Term -> TermID (uint32)
    // 可以使用高性能的 FST (Finite State Transducer) 或简单的 flat map (hash -> termID)
    termDict map[Term]uint32 
    
    // 2. 索引区 (Index / Offsets)
    // 记录每个 TermID 对应的倒排链在数据区的起始位置和长度
    // offsets[termID] = start_index
    // length = offsets[termID+1] - offsets[termID]
    offsets []uint32
    
    // 3. 数据区 (Data Payload)
    // 连续存储所有倒排链的 EntryID
    data []uint64
}
```

#### 构建流程 (Compile Phase)
1.  **Indexing 阶段**：仍然使用临时的 `map[Term][]uint64` 进行数据收集，或者使用 `Builder` 模式收集数据。
2.  **Compile 阶段**：
    *   遍历临时 Map，将所有 `Term` 排序（为了更好的局部性）。
    *   按顺序将 `[]uint64` copy 到全局 `data` 数组中。
    *   记录每个 Term 在 `data` 中的起始 offset。
    *   生成 `termDict` 和 `offsets` 数组。
    *   释放临时 Map，强制 GC。

#### 兼容性设计
*   **接口兼容**：`FlatEntriesHolder` 实现 `EntriesHolder` 接口。
*   **查询兼容**：`GetEntries` 方法通过查 `termDict` -> `offsets` -> 切片 `data`，返回 `EntriesCursor`。
*   **注意**：`EntriesCursor` 目前持有 `[]uint64`，这与 `data[start:end]` 的 slice 也就是 `[]uint64` 完全兼容。无需修改 `EntriesCursor`。

---

## 阶段二：整数压缩与块式迭代 (Integer Compression & Block Iteration)

### 1. 问题分析
*   目前倒排链存储原始 `uint64` (`EntryID`)。
*   **内存带宽瓶颈**：在大规模检索时，内存带宽往往是瓶颈。未压缩的数据意味着需要从内存加载更多字节到 CPU。
*   **存储浪费**：`EntryID` 虽然是 64 位，但高位通常相同（文档 ID 聚类特性）。

### 2. 优化目标
*   引入 SIMD-BP128 (Bit Packing) 或 VByte 算法压缩倒排链。
*   减少内存占用（预期减少 3-4 倍）。
*   利用 SIMD 指令加速解压。

### 3. 技术方案

#### 块式存储结构 (Block Storage)
将倒排链切分为固定大小的块（Block），例如每 128 个整数为一个 Block。

```go
type CompressedHeader struct {
    LastEntryID uint64 // 该块最后一个 ID，用于 SkipTo 快速跳过
    BlockOffset uint32 // 该块数据在 data[] 中的起始字节位置
    Bits        uint8  // 该块使用的位宽 (Bit-width)
}

type CompressedEntriesHolder struct {
    // ... term 索引 ...
    headers []CompressedHeader // 块索引
    data    []byte             // 压缩后的二进制数据
}
```

#### 游标重构 (`EntriesCursor`)
这是本阶段最大的改动，需要重构 `EntriesCursor` 以支持解压。

**当前结构**：
```go
type EntriesCursor struct {
    entries Entries // []uint64
    cursor  int
    // ...
}
```

**新结构 (接口化或适配)**：
为了保持高性能，不建议直接把 `EntriesCursor` 变成 Interface（虚函数开销）。建议修改 `EntriesCursor` 内部实现，使其支持两种模式或专用于压缩模式。

```go
type EntriesCursor struct {
    // 块迭代器
    blockIdx    int
    blockCount  int
    headers     []CompressedHeader
    dataRef     []byte // 引用 holder 的 data
    
    // 当前解压缓冲区 (Buffer)
    decompressedBuffer [128]uint64 
    bufferCursor       int
    bufferSize         int
    
    curEID EntryID
}
```

#### 核心逻辑 (`SkipTo`)
1.  **Block Skip**：检查目标 ID 是否大于 `headers[blockIdx].LastEntryID`。如果是，直接跳过整个 Block（无需解压）。
2.  **In-Block Search**：定位到目标 Block 后，使用 SIMD 指令将 Block 解压到 `decompressedBuffer`，然后在 Buffer 中进行二分或线性查找。

#### 兼容性设计
*   **逻辑兼容**：`SkipTo` 和 `GetCurEntryID` 语义不变。
*   **代码改动**：需要修改 `be_indexer/index_scanner.go` 中的 `EntriesCursor` 定义及方法实现。上层 `FieldCursor` 和 `KGroupsBEIndex` 依赖的是方法签名，只要签名不变，改动局限在 `index_scanner.go` 内部。

---

## 阶段三：混合索引与 SIMD 求交 (Hybrid Indexing & SIMD Intersection)

### 1. 问题分析
*   **长尾分布**：某些 Term 极其稀疏，某些 Term 极其稠密（如 Stop Words 或热门标签）。
*   **标量计算瓶颈**：目前的求交逻辑（Intersection）是标量（Scalar）代码，一次处理一个 ID。

### 2. 优化目标
*   **混合存储**：对稠密 Term 使用 Bitmap，对稀疏 Term 使用压缩 List。
*   **向量化计算**：利用 AVX2/AVX512 指令集一次比较多个 ID。

### 3. 技术方案

#### SIMD Intersection (Vectorized SkipTo)
在 `FieldCursor` 层面（多路归并）或 `EntriesCursor` 层面优化。

*   **场景**：查找多个倒排链的交集。
*   **算法**：
    *   加载 4 个或 8 个 `EntryID` 到 SIMD 寄存器。
    *   执行 `_mm256_cmpgt_epi64` 等指令并行比较。
    *   快速算出谁是最小的 ID，或者是否有共同的 ID。

#### 混合索引结构
在 `Compile` 阶段分析 Term 的分布：
*   **Dense Term**：如果 `len(posting) > Threshold`，尝试构建 Roaring Bitmap（需解决 `EntryID` 64位问题，可能需退化为仅存 DocID 的 Bitmap 作为一级过滤）。
*   **Sparse Term**：维持阶段二的压缩 Block 结构。

由于 `EntryID` 包含 `Size/Index` 等元信息，标准的 Roaring Bitmap (32位) 不适用。
**修正方案**：对于 Dense Term，提取其 `DocID` 存入 Bitmap。
*   查询时，先通过 Bitmap 快速判断 `DocID` 是否存在。
*   如果存在，再回查原始（压缩）倒排链获取完整的 `EntryID` (包含 Index/Size)。
*   这相当于加了一层 **Bloom Filter** 或 **Pre-filter**。

#### 兼容性设计
*   这是一个内部优化，对外接口完全一致。
*   需要引入 Go Assembly 或使用 `klauspost/compress` 等库提供的 SIMD 原语。

---

## 总结与实施建议

| 阶段 | 核心改动 | 复杂度 | 收益 | 兼容性风险 |
| :--- | :--- | :--- | :--- | :--- |
| **Phase 1** | Map -> Flat Arrays | 低 | 显著降低 GC，内存减少 ~30% | 无 (Holder 内部实现) |
| **Phase 2** | 引入 Block 压缩, 重构 Cursor | 中 | 内存减少 ~70%，提升带宽效率 | 低 (需修改 Cursor 结构) |
| **Phase 3** | SIMD 算法, 混合索引 | 高 | 查询吞吐提升 2-5 倍 | 无 (纯算法优化) |

**建议实施顺序**：
1.  优先实施 **Phase 1**。这是一个纯工程重构，风险小，收益立竿见影（特别是针对海量 Term 的场景）。
2.  在 Phase 1 稳定后，实施 **Phase 2**。这需要引入压缩库（如 `encoding/binary` 的 Varint 或第三方 SIMD 库）。
3.  **Phase 3** 属于专家级优化，视具体的性能 Profile 结果决定是否实施。

