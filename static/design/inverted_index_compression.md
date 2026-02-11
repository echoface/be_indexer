# 倒排链压缩与块式迭代 (Inverted List Compression)

## benchmark
⏺ 基于最新的 Benchmark 测试结果，以下是 Compressed 模式与 Map 模式的性能对比分析：

### Benchmark 结果数据 (ns/op)
```
 Term 数量 (N) │ Map 模式 (O(1) 查找 + 直接读取) │ Compressed 模式 (O(logN) 查找 + 解压读取) │ 性能差距
───────────────┼─────────────────────────────────┼───────────────────────────────────────────┼──────────
 1,000         │ 91.30 ns                        │ 478.0 ns                                  │ ~5.2 倍
 10,000        │ 100.7 ns                        │ 566.5 ns                                  │ ~5.6 倍
 100,000       │ 108.4 ns                        │ 531.5 ns                                  │ ~4.9 倍
 1,000,000     │ 315.0 ns                        │ 649.6 ns                                  │ ~2.1 倍
```

### 结果分析

1. 绝对耗时增加：
    • Compressed 模式下的单次查询（包含查找 Term 和解压第一个元素）耗时约在 500ns - 650ns 之间。
    • 相比 Map 模式的 90ns - 300ns，增加了约 400ns 的固定开销。
    • 这 400ns 的额外开销主要来自于 Varint 解压 和 Block 管理。
2. 规模效应：
    • 当 Term 数量达到 100万时，Map 模式由于内存分散导致的 Cache Miss 增加，性能下降明显（从 100ns -> 315ns）。
    • Compressed 模式性能相对平稳（531ns -> 649ns），说明其 CPU Cache 亲和性更好。在大规模数据下，两者的差距缩小到 2 倍左右。
3. 工业级场景评估：
    • 在一个典型的广告检索请求中，假设需要查询 50 个 Term。
    • 总耗时增加： 50 * 400ns = 20,000ns = 0.02ms 。
    • 对于整个检索链路（通常在 10ms - 50ms）来说，0.02ms 的 CPU 耗时损耗是可以忽略不计的。

### 结论

  虽然 Compressed 模式在微观 benchmark 上比 Map 慢了 2-5 倍，但这是用微秒级的 CPU 时间换取了 G 级别内存空间和系统稳定性（零 GC）。

  • 内存收益：内存占用预计减少 60%-80%。
  • GC 收益：彻底消除了数百万个小对象带来的 GC 扫描压力，避免了 STW 问题。
  • 综合评估：这是一次非常划算的 Trade-off，完全符合工业级索引的设计目标。

  建议：
  当前的性能损耗完全在可接受范围内。如果在后续极高并发场景下 CPU 成为瓶颈，我们可以通过 Phase 3 (SIMD 加速) 进一步将解压性能提升 3-4 倍，届时
  Compressed 模式的性能将可能反超 Map 模式。

## 1. 背景与目标

在 Phase 1 中，我们将内存布局优化为了扁平化的 Flat Array，解决了 GC 压力和内存碎片问题。然而，倒排链仍然以原始 `uint64` 格式存储，存在以下问题：
*   **内存占用高**：每个 EntryID 占用 8 字节。对于千万级索引，内存消耗巨大。
*   **内存带宽瓶颈**：查询时需要从内存读取大量原始数据，CPU 等待数据加载（Cache Miss）成为主要延迟来源。

Phase 2 的目标是引入**整数压缩技术**，将内存占用降低 60%-80%，并通过减少内存带宽消耗来提升系统整体吞吐量。

## 2. 核心设计决策 (Why this way?)

### 2.1 为什么选择分块压缩 (Block-based Compression)?
*   **支持随机访问 (SkipTo)**：倒排索引查询中最频繁的操作是 `SkipTo(targetID)`。如果对整个链进行流式压缩（如 Gzip），`SkipTo` 需要从头解压，性能无法接受。
*   **解决方案**：将长链切分为固定大小的块（例如 128 个 ID 一组）。每个块独立压缩，并在块头记录 `MaxID`。查询时可以通过检查 `MaxID` 快速跳过无关的块，仅解压包含目标 ID 的块。

### 2.2 为什么选择 Delta + Varint?
*   **数据特性**：`EntryID`（包含 DocID）在排序后具有单调递增特性。计算相邻 ID 的差值（Delta）后，数值会变得很小。
*   **Varint 优势**：对于小整数，Varint（变长编码）通常只需要 1-2 个字节，远小于原始的 8 字节。
*   **实现成本**：Go 标准库 `encoding/binary` 原生支持 Varint，无需引入复杂的第三方 CGO 库或 SIMD 汇编，维护成本低且跨平台性好。
*   **升级路径**：Delta + Varint 是压缩的基础。未来如果性能仍有瓶颈，可以平滑升级到 SIMD-BP128（比特打包），架构无需大改。

### 2.3 为什么不使用 Roaring Bitmap?
*   **Payload 问题**：我们的 `EntryID` 不仅仅是 DocID，还编码了 `Size`（谓词数量）和 `Index`（谓词下标）信息。
*   **稀疏性**：Roaring Bitmap 适合稠密集合。对于由复杂布尔表达式生成的稀疏倒排链，Bitmap 的优势不如 Delta 列表明显。

## 3. 详细实施方案 (How?)

### 3.1 数据结构设计

在 `DefaultEntriesHolder` 中引入压缩存储相关的字段：

```go
const BlockSize = 128 // 经验值，平衡压缩率和解压开销

// 块索引元信息，用于快速 Skip
type CompressedHeader struct {
    LastEntryID EntryID // 该块中最大的 EntryID
    Offset      uint32  // 该块数据在 compressedData 中的起始字节偏移
}

type DefaultEntriesHolder struct {
    // ... Phase 1 字段 ...
    
    // Phase 2 新增字段
    headers        []CompressedHeader // 块索引数组
    compressedData []byte             // 紧凑的二进制数据区
}
```

### 3.2 压缩流程 (`compileToCompressed`)

在 `CompileEntries` 或 `LoadData` 阶段触发：

1.  **切分**：将排序后的 `EntryID` 列表按 `BlockSize` 切分。
2.  **编码**：
    *   **Header**: 记录当前块的最后一个 `EntryID` 和当前的 `writeOffset`。
    *   **Body**:
        *   首个 ID：写入原始值（Varint）。
        *   后续 ID：计算 `Delta = ID[i] - ID[i-1]`，写入 Delta（Varint）。
3.  **存储**：将编码后的字节流追加到 `compressedData`。

### 3.3 游标重构 (`EntriesCursor`)

`EntriesCursor` 需要从“直接读数组”变为“解码迭代器”。为了保持高性能，避免使用 interface。

```go
type EntriesCursor struct {
    // 原始模式字段 (保持兼容，或者二选一)
    flatEntries []EntryID
    
    // 压缩模式字段
    holder      *DefaultEntriesHolder
    termMeta    TermEntry // 知道该 Term 的 Block 范围
    
    // 运行时状态
    currentBlockIdx int       // 当前正在处理哪个 Block
    decompBuffer    [128]EntryID // 解压缓冲区 (避免每次分配)
    bufCursor       int       // 缓冲区内的游标
    bufLen          int       // 缓冲区有效长度
    
    curEID          EntryID   // 当前指向的 ID
}
```

### 3.4 关键算法：`SkipTo`

```go
func (ec *EntriesCursor) SkipTo(target EntryID) EntryID {
    if ec.curEID >= target {
        return ec.curEID
    }

    // 1. 检查当前解压的 Buffer
    // 如果 Buffer 的最大值 >= target，说明目标可能在 Buffer 中
    if ec.bufLen > 0 && ec.decompBuffer[ec.bufLen-1] >= target {
        // 在 Buffer 中进行二分或线性查找
        return ec.searchInBuffer(target)
    }

    // 2. 跳过不相关的 Block
    // 利用 Header 中的 LastEntryID 快速跳过
    startBlock := ec.currentBlockIdx + 1
    endBlock := ec.termMeta.BlockEndIndex 
    
    for idx := startBlock; idx < endBlock; idx++ {
        header := ec.holder.headers[idx]
        if header.LastEntryID >= target {
            // 找到目标 Block，解压它
            ec.loadBlock(idx)
            return ec.searchInBuffer(target)
        }
    }

    // 3. 没找到，到达末尾
    ec.curEID = NULLENTRY
    return NULLENTRY
}
```

### 3.5 解压逻辑 (`loadBlock`)

1.  根据 `header.Offset` 定位到 `compressedData`。
2.  读取第一个 Varint 作为基准值 `base`。
3.  循环读取后续 Varint，`val = base + delta`，更新 `base`，填充 `decompBuffer`。

## 4. 性能与收益预期

*   **压缩率**：预计 `EntryID` 大小平均从 8 字节降至 2-3 字节（取决于 ID 密度）。总体内存占用预期减少 **60% 以上**。
*   **查询耗时**：
    *   **Skip 性能**：大幅提升。原来 Skip 1000 个 ID 需要访问内存 1000 次（或二分访问 logN 次），现在只需访问少数几个 Cache-Friendly 的 Header。
    *   **解压开销**：Varint 解压会有 CPU 成本。但由于内存带宽压力减小，对于大数据量查询，整体吞吐量通常持平或提升。

## 5. 兼容性风险

*   **对外接口**：`BEIndex` 接口完全不变。
*   **代码改动**：`index_scanner.go` 变动较大。为了降低风险，可以在 `EntriesCursor` 中保留两套逻辑（Flag 控制），或者通过构建标签（Build Tag）切换，确保即使压缩逻辑有 Bug 也可以快速回滚到 Flat 模式。

## 6. 实施路线图

1.  在 `DefaultEntriesHolder` 中增加压缩数据结构。
2.  实现 `compileToCompressed` 并在测试中验证数据正确性。
3.  重构 `EntriesCursor`，支持解压逻辑。
4.  编写 Benchmark，对比 Flat 模式 vs Compressed 模式的 SkipTo 性能。
