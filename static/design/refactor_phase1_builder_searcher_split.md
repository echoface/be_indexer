# Phase 1 设计文档：Builder/Searcher 分离与接口定义

## 1. 背景与问题
当前的 `EntriesHolder` 接口承载了过多的职责：
1.  **数据写入**：`CommitFieldIndexingData`
2.  **数据编译**：`CompileEntries`
3.  **数据查询**：`GetEntries`
4.  **序列化**：`Serialize/Deserialize`

这种设计导致了以下问题：
*   **内存管理困难**：写入时的 Map 结构和查询时的 Flat 结构混合在同一个 Struct 中，GC 压力大。
*   **并发模型复杂**：必须通过 `CompileEntries` 显式切换状态，无法支持写入的同时进行查询（除非加全局锁）。
*   **扩展性差**：难以单独优化查询端的存储结构（例如 mmap）。

## 2. 设计目标
将索引的生命周期拆分为两个明确的阶段和组件：
1.  **SegmentBuilder (Write-Optimized)**: 负责构建索引，使用堆内存，注重写入性能。
2.  **SegmentReader (Read-Optimized)**: 负责查询索引，使用紧凑内存或 mmap，注重查询性能和低内存占用。

## 3. 核心接口定义

### 3.1 Segment
`Segment` 是数据管理的最小单元。一个 Segment 代表一批文档的索引数据。

```go
type Segment interface {
    // ID 返回 Segment 的唯一标识
    ID() uint64
    
    // NumDocs 返回该 Segment 包含的文档数量
    NumDocs() int
    
    // Reader 返回该 Segment 的查询接口
    Reader() (SegmentReader, error)
    
    // Close 释放资源
    Close() error
}
```

### 3.2 SegmentBuilder (Writer)
用于构建一个新的 Segment。

```go
type SegmentBuilder interface {
    // AddDocument 添加一个文档到构建器
    // 内部会将文档拆解为 Field/Term/DocID
    AddDocument(doc *Document) error
    
    // Flush 将内存中的数据冻结并序列化，生成不可变的 SegmentReader
    // 这里的 output 可以是 io.Writer (写入磁盘) 或 buffer (内存)
    Flush(output io.Writer) error
    
    // Reset 重置构建器状态，以便复用
    Reset()
}
```

### 3.3 SegmentReader (Reader)
用于对不可变的 Segment 进行查询。

```go
type SegmentReader interface {
    // GetPostings 获取指定 Field/Value 的倒排链迭代器
    // 注意：这里将来会升级为 TermID，目前 Phase 1 仍保持 Field/Value
    GetPostings(field BEField, value any) (PostingIterator, error)
    
    // Contains 检查文档是否存在 (用于过滤删除的文档)
    Contains(id DocID) bool
    
    // Close 释放读取资源 (如关闭 mmap)
    Close() error
}
```

## 4. 重构方案：DefaultKVHolder 的拆解

现有的 `DefaultKVHolder` (实际是 `CompressedKVHolder`) 将被拆解。

### 4.1 MemSegmentBuilder
*   **存储结构**: 维持现有的 `map[KVTerm][]EntryID` 或 `buf []termEID` 结构。
*   **行为**: 
    *   `AddDocument` 时，解析文档，生成 Term 和 EntryID，追加到 buf 中。
    *   不做任何压缩或排序，追求极致的写入速度。

### 4.2 DiskSegmentReader (或 CompactMemoryReader)
*   **存储结构**: 现有的 `util.FlatMap` 或将来的二进制块。
*   **行为**:
    *   在 `Flush` 阶段，Builder 将数据排序、压缩、写入一段连续的 `[]byte`。
    *   Reader 初始化时，解析这段 Header，或者直接映射指针。
    *   `GetPostings` 使用二分查找 + `BlockIterator`。

## 5. 迁移计划

1.  定义 `segment.go`，包含上述接口。
2.  实现 `MemBuilder`，迁移原 `CompressedKVHolder` 的写入逻辑。
3.  实现 `FlatReader`，迁移原 `CompressedKVHolder` 的 `GetEntries` 和 `findTermIndex` 逻辑。
4.  在 `BEIndex` 中，不再持有 `map[BEField]EntriesHolder`，而是持有：
    *   `activeSegment SegmentBuilder`: 当前正在写入的段。
    *   `sealedSegments []SegmentReader`: 已经构建好的只读段。
5.  查询时，遍历 `sealedSegments` 和 `activeSegment` (如果支持 NRT)，合并结果。

## 6. 带来的收益
*   **GC 友好**: `Flush` 之后，Builder 产生的大量小对象被释放，Reader 是一整块内存，无指针。
*   **并发安全**: Reader 是不可变的，天然线程安全。
*   **持久化准备**: `Flush(io.Writer)` 接口天然支持将索引刷写到磁盘文件。
