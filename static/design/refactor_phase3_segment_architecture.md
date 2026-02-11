# Phase 3 设计文档：Segment 架构与增量更新

## 1. 背景与问题
当前 `be_indexer` 不支持增量更新。
*   **全量构建痛点**: 每次有新数据，都需要重新构建整个索引。对于亿级数据，这需要数小时，导致数据更新延迟（Latency）极高。
*   **内存瓶颈**: 全量构建需要一次性将所有数据加载到内存，限制了单机能处理的数据量。

## 2. 设计目标
1.  **支持增量更新 (NRT)**: 新文档可以在秒级/毫秒级内被检索到。
2.  **支持物理删除**: 提供文档删除机制。
3.  **降低内存峰值**: 将大索引拆分为多个小 Segment，分批构建。

## 3. 架构设计：LSM-Tree 思想

借鉴 LSM-Tree (Log-Structured Merge Tree) 和 Lucene 的架构。

### 3.1 核心组件

#### SegmentManager
负责维护当前所有的 Segment 视图。
```go
type SegmentManager struct {
    mutable   SegmentBuilder   // 当前活跃的内存 Buffer
    freezing  SegmentBuilder   // 正在被 Flush 的 Buffer (只读)
    sealed    []SegmentReader  // 已经持久化的只读 Segments
    
    lock      sync.RWMutex
}
```

#### Document ID 管理
*   **Global DocID**: 外部系统使用的 ID (如 string uuid)。
*   **Internal DocID**: 索引内部使用的递增 int64。
*   **Mapping**: 需要维护 `Global -> Internal` 的映射（可能通过 BloomFilter 或外部 KV 存储）。
*   **Scope**: 在 Phase 3 初期，建议 `Internal DocID` 在整个 Indexer 范围内唯一且递增，而不是 Per-Segment。这简化了全局 BitSet 的实现。

#### Deletion Policy (Tombstones)
*   维护一个全局的 `RoaringBitmap`，记录被删除的 `Internal DocID`。
*   检索时，所有 Segment 返回的结果集，最后统一执行 `AndNot(DeletedBitmap)`。

### 3.2 写入流程 (Write Path)

1.  **Append**: 新文档 `AddDocument` 进入 `mutable` Segment。
2.  **Retrieval**: 查询请求同时发给 `mutable` 和 `sealed` Segments。
3.  **Flush**: 
    *   当 `mutable` 大小达到阈值 (e.g. 100MB 或 10w docs)。
    *   将 `mutable` 切换为 `freezing`，并创建新的 `mutable`。
    *   后台线程将 `freezing` 序列化、构建 FST、压缩，生成新的 `SegmentReader`。
    *   完成后，将新 Reader 加入 `sealed` 列表，丢弃 `freezing`。

### 3.3 检索流程 (Read Path)

1.  **Fan-out**: 查询请求并行（或串行）发送给 `mutable` 和所有 `sealed` Segments。
2.  **Collect**: 每个 Segment 返回符合条件的 `DocIDList` (或 Iterator)。
3.  **Merge**: 将所有结果集进行逻辑 `OR` (Union) 合并。
    *   `be_indexer` 的场景通常是 Boolean 检索，所以是 Union。
4.  **Filter**: 过滤掉在 `DeletedBitmap` 中的 DocID。

### 3.4 压缩与合并 (Compaction)

随着时间推移，`sealed` 列表会包含大量小 Segment，导致查询性能下降（需要遍历太多文件）。

*   **Merge Policy**: 类似于 Tiered Merge Policy。
    *   当发现有 N 个大小相近的小 Segment 时，触发合并。
*   **Merge Process**:
    *   创建一个新的 `SegmentBuilder`。
    *   遍历要合并的 Segments，读取出所有有效文档（跳过被删除的）。
    *   重新构建一个新的大 Segment。
    *   原子替换：从 `sealed` 列表中移除旧的 Segments，加入新的 Segment。

## 4. 接口变更

**MultiSegmentIndex**:
```go
type MultiSegmentIndex struct {
    manager *SegmentManager
}

func (idx *MultiSegmentIndex) Retrieve(query Queries) (DocIDList, error) {
    // 1. Snapshot当前视图
    segments := idx.manager.AcquireView()
    defer idx.manager.ReleaseView()
    
    // 2. 并发查询
    var results []DocIDList
    for _, seg := range segments {
        res, _ := seg.Reader().Retrieve(query)
        results = append(results, res)
    }
    
    // 3. 合并与过滤
    final := Union(results...)
    final.Remove(idx.manager.GetDeletes())
    
    return final, nil
}
```

## 5. 收益
*   **低延迟**: 写入即进入内存表，立即可查。
*   **可扩展**: 内存不够时 Flush 到磁盘，支持远超内存大小的数据集（依赖 mmap 读取）。
*   **稳定性**: 内存使用峰值受控，不再随数据量线性增长。
