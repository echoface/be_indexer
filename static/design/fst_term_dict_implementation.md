# FST Term Dictionary 技术实现方案

## 1. 概述
本方案旨在实现基于 **Finite State Transducer (FST)** 的 Term Dictionary，以替代目前的 `DefaultTermDict` (Sorted Array)。FST 能够对 Term 的前缀和后缀进行极致压缩，显著降低内存占用，同时保持 O(Length) 的查询复杂度。

本方案不考虑对旧版本索引的兼容性。

## 2. 核心设计
FST 本质上是一个**有向无环图 (DAG)**，它将 Key (Term) 映射为 Value (TermID)。由于我们的 Term 是按字典序插入的，我们可以使用 **线性构建算法** (O(N)) 来构建最小 FST。

### 2.1 映射关系
*   **Input**: `[FieldID (Uvarint)] [Term Bytes]` (组合 Key，利用前缀压缩)
*   **Output**: `TermID` (uint64)

### 2.2 二进制存储格式 (Binary Format)
为了最大化压缩率，我们参考 Lucene 和 Vellum 的设计，采用变长编码存储 FST 节点。

#### 2.2.1 寻址
FST 从后向前写入 (Reverse Order)，Root 节点最后写入。地址指在 byte slice 中的偏移量。

#### 2.2.2 节点 (State) 格式
每个节点包含：
1.  **Flags (1 byte)**: 标识节点类型、是否 Final、是否有 Final Output。
2.  **Final Output (Uvarint, Optional)**: 当节点为 Final 时，该 Key 对应的 Value 增量。
3.  **Transitions (边)**: 边的列表。

**节点类型 (Flags 低 4 位)**:
*   `TYPE_EMPTY (0x00)`: 无出边 (Leaf)。
*   `TYPE_LINEAR (0x01)`: 少量出边 (< 4)，线性存储。
*   `TYPE_SPARSE (0x02)`: 中量出边，有序存储，支持二分查找。
*   `TYPE_DENSE (0x03)`: 大量出边 (密集)，使用 bitset + 数组索引 (256 大小)。

**Transitions 格式**:
*   `Label`: byte (1 字节)
*   `Output`: Uvarint (边的输出值)
*   `Target`: Uvarint (目标节点地址，由于是从后向前写，通常存储 `CurrentPos - TargetPos` 的差值以压缩)

### 2.3 构建算法 (Builder)
采用 **Mastiff / Lucene** 风格的线性构建算法：
1.  维护一个 **Frontier (未编译路径)**，存储当前 Key 的路径节点。
2.  当插入新 Key 时：
    *   计算新 Key 与上一个 Key 的 **最长公共前缀 (LCP)**。
    *   **Freeze (冻结)**: 将 LCP 之后的后缀路径上的节点从 Frontier 中弹出，进行序列化并写入底层 `[]byte`。
    *   **Registry (去重)**: 在写入前，检查 Hash Map 中是否已存在相同结构的节点。如果存在，复用已有节点地址 (DAG 压缩的核心)。
    *   将新 Key 的后缀部分加入 Frontier。
3.  所有 Key 插入完毕后，冻结剩余的 Frontier，最后写入 Root 节点。

### 2.4 查询算法 (Lookup)
1.  从 Root 节点开始。
2.  对于 Key 的每一个 byte，在当前节点的 Transitions 中查找匹配的边 (Linear Scan, Binary Search, or Direct Index)。
3.  如果找到，累加边的 Output，移动到 Target 节点。
4.  如果 Key 结束：
    *   检查当前节点是否标识为 `Final`。
    *   如果是，Result = Accumulated Output + Final Output。
    *   否则，Key 不存在。

### 2.5 反向查询 (Decode: TermID -> Term)
FST 原生不支持高效的反向查询。考虑到 `Decode` 主要用于 Debug，且为了节省内存，我们采用 **采样索引 (Sampled Index)** 方案。

*   **Sampled Index**: 每隔 `K` 个 Term (例如 K=32)，存储一个完整的 Key 和它对应的 TermID。
*   **Decode 流程**:
    1.  在 Sampled Index 中二分查找找到不大于 TargetID 的最大 Sample。
    2.  从该 Sample 的 Key 开始，利用 `Automaton` 在 FST 上进行顺序遍历 (Next)。
    3.  跳过 `TargetID - SampleID` 个 Term，即可到达目标 Term。
*   **性能**: O(K * TermLen)。

## 3. 接口定义

```go
package fst

// FSTTermDict 实现 be_indexer.TermDictionary 接口
type FSTTermDict struct {
    data []byte // FST binary data
    root uint64 // Root node address
    
    // 采样索引用于 Decode
    sampleKeys [][]byte
    sampleIDs  []uint64
}

type Builder struct {
    // ...
}

func NewBuilder(w io.Writer) *Builder
func (b *Builder) Insert(key []byte, val uint64) error
func (b *Builder) Finish() error
```

## 4. 关键代码骨架 (POC)

### 4.1 FST 节点编码 (Encoder)

```go
func (n *Node) WriteTo(w io.Writer) (addr int, err error) {
    // 1. Determine Node Type based on len(n.Trans)
    // 2. Write Flags
    // 3. Write Final Output (if Final)
    // 4. Write Transitions (Label, Output, Target)
    // ...
}
```

### 4.2 线性构建 (Builder)

```go
func (b *Builder) Add(key []byte, output uint64) error {
    // 1. Calc LCP with lastKey
    // 2. Compile suffix of lastKey (from len(lastKey) down to LCP+1)
    // 3. Add new suffix to frontier
    // 4. Update lastKey
}

func (b *Builder) compileNode(idx int) {
    node := b.frontier[idx]
    // Dedup: check registry
    if addr, ok := b.registry[node.Hash()]; ok {
        // Reuse node
    } else {
        // Serialize node to bytes
        // Update parent's target to this address
    }
}
```

## 5. 集成计划
1.  在 `be_indexer` 下创建 `fst/` 包。
2.  实现 FST 核心逻辑。
3.  修改 `TermDictionary` 的实现，从 `DefaultTermDict` 切换为 `FSTTermDict`。
4.  修改 `indexstore.proto`，将 `TermDict` 字段改为存放 FST 二进制数据。

