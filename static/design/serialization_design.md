# 索引序列化方案设计 (Index Serialization Design)

## 1. 目标 (Goals)

1.  **持久化 (Persistence)**: 将构建好的内存索引导出为二进制文件。
2.  **分发 (Distribution)**: 支持将索引文件分发到其他机器，并快速恢复为可服务的 `BEIndex` 实例。
3.  **兼容性 (Compatibility)**: 支持所有现有的 Holder 类型（Default, AC, Range）。

## 2. 总体架构 (Architecture)

采用 **"Header + Metadata + Data Blocks"** 的分层存储结构。

### 2.1 存储格式 (Binary Layout)

```text
+-------------------------------------------------------+
| Header (16 bytes)                                     |
| - Magic Number (4 bytes): 0xBE1D2E55                  |
| - Version (4 bytes): 1                                |
| - IndexerType (4 bytes): 0=KGroups, 1=Compact         |
| - Checksum (4 bytes): CRC32 of Metadata+Content       |
+-------------------------------------------------------+
| Metadata Block                                        |
| - Length (8 bytes)                                    |
| - Payload (JSON/Protobuf):                            |
|     - Field Descriptors (Name, Type, HolderType)      |
|     - ID Allocator State (String <-> ID mapping)      |
|     - Settings (BadConjBehavior, etc.)                |
+-------------------------------------------------------+
| Wildcard Block                                        |
| - Length (8 bytes)                                    |
| - Payload: List of EntryIDs (Varint encoded)          |
+-------------------------------------------------------+
| Holder Data Blocks (Repeated)                         |
| +---------------------------------------------------+ |
| | Block Header                                      | |
| | - FieldID (8 bytes)                               | |
| | - HolderType (String: "default", "ac", etc.)      | |
| | - PayloadLength (8 bytes)                         | |
| +---------------------------------------------------+ |
| | Block Payload (Holder Specific Data)              | |
| +---------------------------------------------------+ |
+-------------------------------------------------------+
```

## 3. 组件序列化分析 (Component Analysis)

### 3.1 接口扩展

```go
type EntriesHolder interface {
    // ... 原有方法
    
    // Serialize 导出内部状态
    Serialize(w io.Writer) error
    
    // Deserialize 恢复状态 (部分 Holder 需要触发 Post-Compile)
    Deserialize(r io.Reader) error
}
```

### 3.2 DefaultEntriesHolder

*   **文件**: `holder/default_holder.go`
*   **状态**: `plEntries map[BEValue]Entries`
*   **策略**: 直接序列化 Map。
*   **格式**:
    *   `Count` (Varint)
    *   Repeat `Count`:
        *   `KeyType` (byte) -> `Key` (Varint/String)
        *   `ListLen` (Varint) -> `EntryIDs` (Varint List)

### 3.3 ACEntriesHolder

*   **文件**: `holder/ahoholder/ahocorasick_holder.go`
*   **状态**: `values map[string]Entries` + `machine *aho.Machine`
*   **策略**: **Rebuild 模式**。
    *   `Serialize`: 仅保存 `values` 映射。
    *   `Deserialize`: 恢复 `values`，然后调用 `CompileEntries()` 重建 `aho.Machine`。

### 3.4 RangeHolder / OptimizedRangeHolder

*   **文件**: `holder/rangeholder/`
*   **状态**: `plEntries` + `rangeIdx` (SegmentTree/List)
*   **策略**: **Replay 模式**。
    *   `Serialize`:
        1.  保存 `plEntries` (Map)。
        2.  保存**原始 Range 列表**: `[{Left, Right, EntryID}, ...]`。需在构建时记录。
    *   `Deserialize`:
        1.  恢复 `plEntries`。
        2.  读取 Range 列表，调用内部 `Add` 方法重新插入。
        3.  调用 `CompileEntries()` 重建线段树或链表结构。

## 4. 增量更新与分发 (Incremental)

*   **全量**: 使用上述 `Dump/Load` 机制。
*   **增量**: 继续使用现有的 `DocIdxCache`。主节点将新文档的 `DocIdxCache` 序列化后下发，从节点调用 `AddDocIndexingData` 更新内存状态。

