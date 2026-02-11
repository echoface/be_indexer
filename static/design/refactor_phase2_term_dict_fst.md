# Phase 2 设计文档：Term Dictionary 与 TermID

## 1. 背景与问题
在 Phase 1 中，我们实现了 Segment 的读写分离，但底层的存储结构（`CompressedFlatMap`）仍然直接使用字符串作为 Key (`KVTerm`)。
这导致了以下问题：
1.  **存储冗余**：相同的字符串值（如 "shanghai"）如果在多个 Field 中出现，或者在索引中多次引用，会占用大量空间。
2.  **比较开销**：查询时使用字符串比较，性能低于整数比较。
3.  **内存占用**：Go 语言的 `string` 对象包含指针和长度，且字符串内容分散在堆上，GC 压力大。

## 2. 设计目标
引入 **Term Dictionary (词典)** 和 **TermID** 机制。

*   **TermID 化**：将所有的 `Field=Value` 映射为一个唯一的 `uint64` TermID。
*   **字典存储**：使用高效的数据结构存储 `TermID -> Term String` 的映射（通常只需要 `Term String -> TermID` 用于查询，以及 `TermID -> Postings Offset` 用于倒排查找）。
*   **倒排存储**：倒排索引的 Key 变为 `TermID`，按 `TermID` 排序存储。

## 3. 核心组件设计

### 3.1 TermDictionary 接口

```go
type TermDictionary interface {
    // Get 返回 term 对应的 ID，如果不存在返回 false
    Get(field BEField, value string) (uint64, bool)
    
    // Decdoe 根据 ID 反查 term (可选，主要用于 Debug)
    Decode(id uint64) (BEField, string, bool)
    
    // Size 返回字典中的词条数量
    Size() int
}
```

### 3.2 存储结构选择

#### 方案 A: Sorted Array (MVP)
将所有 Term (`<FieldID, Value>`) 排序后存储在一个紧凑的数组中。
*   **查询**：二分查找 (Binary Search)。复杂度 O(log N * L)，L 为字符串平均长度。
*   **内存**：此时仍然需要存储字符串内容，但可以是一块连续的 `[]byte` + offsets。
*   **TermID**：数组下标即为 TermID (或者显式存储 ID)。为简单起见，使用**数组下标**作为 TermID。

#### 方案 B: FST (Finite State Transducer) (Phase 2.5)
使用 FST 存储字符串。
*   **优势**：前缀共享，极高的压缩率。
*   **劣势**：实现复杂，构建慢。
*   **计划**：先实现方案 A，接口预留 FST 扩展空间。

### 3.3 数据布局 (Protocol Buffers)

我们需要修改 `indexstore.proto`。

**旧结构**：
```protobuf
message CompressedFlatMap {
    repeated FlatMapKey Keys = 1; // 包含 StrKey
    repeated FlatMapHeader Headers = 2;
    bytes Data = 3;
}
```

**新结构**：
```protobuf
message SegmentData {
    // 1. Term Dictionary
    TermDict Dict = 1;
    
    // 2. Postings (Key 是 TermID，即 Dict 中的 Index)
    repeated PostingPtr Postings = 2; 
    
    // 3. Compressed Posting Data (实际的倒排数据块)
    bytes PostingData = 3;
}

message TermDict {
    // 简单的 Block 存储：所有字符串拼接在一起
    bytes Content = 1;
    // 偏移量列表
    repeated uint32 Offsets = 2;
    
    // 或者将来放 FST 的二进制
    bytes FSTData = 3; 
}

message PostingPtr {
    uint64 TermID = 1; // 可省略，如果数组下标一一对应
    uint32 HeaderOffset = 2; // 指向 PostingData 的 Header 索引
    uint32 Length = 3; // 包含的文档数量
}
```

## 4. 构建与查询流程

### 4.1 构建 (MemSegmentBuilder.Flush)
1.  收集 Buffer 中所有的 `KVTerm`。
2.  对 `KVTerm` 进行排序和去重。
3.  构建 `TermDictionary`：
    *   将排序后的 Terms 写入 `TermDict` 结构。
    *   Term 在数组中的 Index 即为其 `TermID`。
4.  构建 Postings：
    *   遍历 Buffer，将 `KVTerm` 替换为 `TermID`。
    *   按照 `TermID` 分组聚合 `EntryID`。
    *   对每个 `TermID` 的 Posting List 进行压缩，写入 `PostingData`。
    *   记录 `PostingPtr`。
5.  序列化 `SegmentData` 到输出流。

### 4.2 查询 (BlockSegmentReader)
1.  加载时，反序列化 `SegmentData`。
2.  初始化 `TermDictionary` (解析 Offsets 或 FST)。
3.  `GetPostings(field, value)`:
    *   调用 `Dict.Get(field, value)` 获取 `TermID`。
    *   如果存在，使用 `TermID` 在 `Postings` 数组中查找 `PostingPtr`（如果是稠密数组，直接下标访问；如果是稀疏，二分查找）。
    *   根据 `HeaderOffset` 初始化 `BlockIterator`。

## 5. 兼容性
Phase 1 的代码使用了 `CompressedFlatMap`。Phase 2 将引入新的格式。
为了平滑过渡，`BlockSegmentReader` 可以通过探测数据头或者版本号来区分加载旧格式还是新格式。
但鉴于我们还在开发初期，可以直接 breaking change，或者保留旧的 struct 定义但不再使用。

为了清晰，建议定义新的 Proto Message，不要复用旧的。
