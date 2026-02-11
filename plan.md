# 混合谓词索引构建方案（Hybrid Predicate Indexing Design）

## 1. 总体架构设计 (Overall Architecture)

### 1.1 核心目标
构建一个高效的内存布尔检索系统，支持复杂的混合谓词（=, >, <, [l,r], Contains）和模式匹配，具备毫秒级低延迟和高并发能力。

### 1.2 索引选择策略 (Container Routing)
根据字段类型和谓词特征，采用**分而治之**的策略，为不同类型的查询选择最优的索引结构：

| 谓词类型 | 典型场景 | 推荐索引结构 (Best Practice) | 理由 (Rationale) |
| :--- | :--- | :--- | :--- |
| **相等 (`=`)** | `Region=CN`, `Tag=Male` | **倒排索引 (Inverted Index) + RoaringBitmap** | 位图集合运算极快，内存占用极低。 |
| **范围 (`>, <, []`)** | `Age>18`, `Price∈[10,100]` | **离散化线段树 (Discretized Segment Tree)** | 将无限连续空间映射为有限区间，查询复杂度 O(log N)，优于 Interval Tree。 |
| **模式匹配 (`Contains`)** | `Title ⊃ "VIP"`, `Content ⊃ "Apple"` | **AC 自动机 (Aho-Corasick Automaton)** | 多模式串一次扫描匹配，时间复杂度 O(N)，N为文本长度，与 Pattern 数量无关。 |

### 1.3 数据流架构
```mermaid
graph TD
    subgraph "Indexer Node (Builder)"
        A[订阅规则源 (DB/Log)] --> B(Parser/Normalizer)
        B --> C{谓词路由}
        C -- "Age=18" --> D[Default Holder<br>(Inverted Index)]
        C -- "Score > 60" --> E[Optimized Range Holder<br>(Segment Tree)]
        C -- "Title has 'News'" --> F[AC Holder<br>(AC Automaton)]
        D & E & F --> G[Index Compiler]
        G --> H[Serialize to .idx (Binary/Proto)]
    end

    subgraph "Distribution Layer"
        H --> I[Object Storage (S3/HDFS)]
        I --> J[Searcher Nodes (Retriever)]
    end

    subgraph "Searcher Node (Retriever)"
        K[用户请求<br>{Age:25, Title:"..."}] --> L(Tokenizer)
        L --> M{查询路由}
        
        M -- "Age:25" --> N[查 Range Index<br>Find Segment -> Collect Bitmap]
        M -- "Title:..." --> O[跑 AC Automaton<br>Output Keywords -> Collect Bitmap]
        
        N & O --> P[Bitset Intersection (Roaring)]
        P --> Q[Result DocIDs]
    end
```

---

## 2. 核心组件详细设计

### 2.1 混合范围索引 (Hybrid Range Indexing)
**目标**：解决传统 Interval Tree 内存碎片大、缓存不友好的问题。
**方案**：**坐标压缩 (Coordinate Compression) + 隐式线段树 (Implicit Segment Tree)**。

#### 核心算法 (Pseudo-Code)
1.  **构建阶段 (Build Phase)**：
    *   收集所有订阅规则中涉及的边界值（例如 `Age > 18`, `Age < 60`, `Age in [25, 30]` -> `Boundaries: [18, 25, 30, 60]`）。
    *   将连续数值空间离散化为有限个**基础区间 (Elementary Intervals)**。
    *   构建线段树，每个节点代表一个聚合区间，挂载一个 RoaringBitmap，存储覆盖该区间的订阅 ID。

```go
// 伪代码：构建过程
type SegmentTree struct {
    Boundaries []int64          // 压缩后的坐标
    Nodes      []*SegmentNode   // 线段树节点数组（扁平化存储）
}

type SegmentNode struct {
    Bitmap *roaring.Bitmap      // 覆盖该节点的订阅 ID 集合
}

func (tree *SegmentTree) AddRange(start, end int64, subID uint32) {
    // 1. 坐标映射：将原始数值映射为离散索引
    lIdx := tree.FindBoundaryIndex(start)
    rIdx := tree.FindBoundaryIndex(end)
    
    // 2. 线段树插入：分解为 O(log N) 个节点
    tree.insert(root, lIdx, rIdx, subID)
}

func (node *SegmentNode) insert(l, r int, subID uint32) {
    if node.CoveredBy(l, r) {
        node.Bitmap.Add(subID)
        return
    }
    // 递归子节点...
}
```

2.  **查询阶段 (Query Phase)**：
    *   将查询值（如 `Age=28`）映射到离散区间索引。
    *   从叶子节点向上回溯至根节点，收集路径上所有节点的 Bitmap。
    *   计算并集（Union）得到结果。

```go
// 伪代码：查询过程
func (tree *SegmentTree) Query(value int64) *roaring.Bitmap {
    // 1. 找到值所在的离散区间
    idx := tree.FindBoundaryIndex(value)
    
    // 2. 收集路径上的所有 Bitmap
    result := roaring.New()
    node := tree.GetLeafNode(idx)
    for node != nil {
        result.Or(node.Bitmap) // 路径上的所有规则都满足
        node = node.Parent
    }
    return result
}
```

#### 业界对比与选型理由
*   **vs Threshold List**: Threshold List (有序数组) 对于 `>` 和 `<` 查询非常快（二分查找），但在处理闭区间 `[L, R]` 时需要维护两个列表并取交集，且难以处理复杂的区间重叠。
*   **vs Interval Tree**: 传统指针式 Interval Tree 内存分散，遍历时 Cache Miss 高。
*   **本方案 (Segment Tree)**: 通过坐标压缩将空间有限化，线段树结构稳定，查询复杂度固定为 O(log K)，且非常适合并行化和 RoaringBitmap 集成。

### 2.2 模式匹配索引 (Pattern Match Index)
**目标**：支持 `Content Contains "Keyword"` 类型的订阅。
**方案**：**AC 自动机 (Aho-Corasick)**。

#### 核心流程
1.  **构建**：将所有订阅规则中的关键词（如 "Apple", "Huawei"）构建为 AC 自动机。每个关键词关联一个 Bitmap（存储订阅 ID）。
2.  **查询**：
    *   对输入文档进行分词或直接流式输入 AC 自动机。
    *   AC 自动机输出命中的关键词 ID。
    *   查表获取对应的订阅 ID Bitmap。
    *   合并所有 Bitmap。

```go
// 伪代码：AC 查询
func (ac *ACIndex) Query(text string) *roaring.Bitmap {
    result := roaring.New()
    
    // AC 自动机流式匹配
    matches := ac.Machine.FindAll(text) 
    
    for _, match := range matches {
        keywordID := match.ID
        // 获取订阅了该关键词的 Bitmap
        subBitmap := ac.InvertedIndex[keywordID]
        result.Or(subBitmap)
    }
    
    return result
}
```

---

## 3. 序列化与分发 (Serialization & Distribution)

### 3.1 零拷贝加载 (Zero-Copy Loading)
为了支持大规模索引的快速加载（秒级启动），放弃基于 Go Heap 的对象反序列化，转而使用 **mmap** 友好的二进制格式。

**文件布局 (Binary Layout)**:
```
[Header: Magic | Version | Checksum]
[Section 1: Dictionary (String -> ID)]
[Section 2: Segment Tree Data (Flat Array)]
[Section 3: AC Automaton (Double-Array Trie)]
[Section 4: Roaring Bitmaps (Serialized Bytes)]
```

*   **Builder**: 使用 Protobuf 或自定义编码生成上述紧凑二进制块。
*   **Searcher**: 使用 `mmap` 系统调用将文件映射到虚拟内存。直接通过指针偏移访问数据，无须解析为 Go 对象，极大减少 GC 压力。

---

## 4. 实施计划 (Implementation Roadmap)

### Phase 1: 核心结构优化 (当前重点)
1.  完善 `OptimizedRangeHolder`，引入 **RoaringBitmap** 替换 `[]EntryID`。
2.  实现 **Hybrid Storage Strategy**：对于小范围（如 `Size < 10`），直接展开为倒排索引，不走线段树。

### Phase 2: 序列化与分发
1.  定义统一的 `.idx` 文件格式。
2.  实现 `Indexer.Dump(io.Writer)` 和 `Indexer.Load(io.Reader)`。
3.  验证序列化后的文件大小和加载速度。

### Phase 3: 全链路集成测试
1.  构建包含混合谓词（=, >, <, Contains）的复杂测试用例。
2.  进行基准测试 (Benchmark)，对比优化前后的内存占用和 QPS。

## 5. 总结建议
针对您的需求，**“坐标压缩 + 线段树”** 是处理混合范围查询的最佳平衡点，它结合了离散化处理的灵活性和树形结构的高效性。配合 **AC 自动机** 处理文本模式匹配，以及 **RoaringBitmap** 处理集合运算，可以构建一个工业级的高性能布尔检索引擎。
