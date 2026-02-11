# TermIterator 架构改进方案

## 1. 现状与问题分析

### 现状
目前 `TermIterator` (即 `PostingIterator`) 的核心接口定义简洁，符合倒排链迭代器设计：

```go
type TermIterator interface {
    Current() EntryID
    SkipTo(target EntryID) EntryID
}
```

但实现层 `EntriesCursor` 存在以下问题：

1.  **结构体臃肿 (Fat Struct)**: 
    *   `EntriesCursor` 同时包含 `[]EntryID` (Raw Mode) 和 `[]byte` + `decompBuffer` (Compressed Mode) 的字段。
    *   无论使用哪种模式，都会分配所有字段（包括 128 大小的 `decompBuffer` 数组），导致内存浪费。

2.  **逻辑耦合**:
    *   `SkipTo` 方法内部通过 `if ec.compressedData != nil` 进行运行时分支判断。
    *   不同的存储后端（Slice vs Compressed Block）逻辑混杂在一起，难以维护和优化。

3.  **扩展性受限**:
    *   `RoaringEntriesCursor` 目前只是占位符。
    *   若要支持 RoaringBitmap，必须在 `EntriesCursor` 中再增加 Bitmap 相关字段，进一步加剧结构体膨肿。

## 2. 改进目标

1.  **解耦实现**: 将 `Raw Mode`、`Compressed Mode` 和 `RoaringBitmap Mode` 拆分为独立的结构体实现。
2.  **消除分支**: 利用 Go 的接口多态特性，移除 `SkipTo` 等热点路径中的运行时 `if/else` 判断。
3.  **内存优化**: 各实现仅持有必要的字段，减少内存占用。
4.  **原生支持 Roaring**: `RoaringIterator` 应直接操作 Bitmap，而非退化为数组。

## 3. 详细设计方案

### 3.1 接口保持不变
核心接口 `TermIterator` 不需要修改，它已经足够通用。

```go
type TermIterator interface {
    Current() EntryID
    SkipTo(target EntryID) EntryID
}
```

### 3.2 实现拆分

我们将 `EntriesCursor` 拆分为三个独立的实现：

#### A. SliceIterator (原 Raw Mode)
仅处理内存中的 `[]EntryID` 切片。适用于小数据量或未压缩的场景。

```go
type SliceIterator struct {
    key     QKey
    entries Entries
    cursor  int
    curEID  EntryID
}

func NewSliceIterator(key QKey, entries Entries) *SliceIterator {
    it := &SliceIterator{
        key:     key,
        entries: entries,
        cursor:  0,
        curEID:  NULLENTRY,
    }
    if len(entries) > 0 {
        it.curEID = entries[0]
    }
    return it
}

func (it *SliceIterator) Current() EntryID {
    return it.curEID
}

func (it *SliceIterator) SkipTo(target EntryID) EntryID {
    if it.curEID >= target {
        return it.curEID
    }
    // 使用二分查找或 Galloping Search 优化
    // ... (现有逻辑的简化版，去除 Compressed 分支)
    return it.curEID
}
```

#### B. BlockIterator (原 Compressed Mode)
处理分块压缩的数据。持有解压 Buffer。

```go
type BlockIterator struct {
    key            QKey
    headers        []util.CompHeader
    compressedData []byte
    
    // 状态字段
    currentHeader  int // 当前块索引
    headerEnd      int // 结束块索引
    
    // 解压缓冲
    decompBuffer   [128]EntryID 
    bufCursor      int
    bufLen         int
    
    curEID         EntryID
}

func NewBlockIterator(...) *BlockIterator {
    // ... 初始化并加载第一个 Block
}

func (it *BlockIterator) SkipTo(target EntryID) EntryID {
    // 1. 检查当前 Buffer
    // 2. 检查 Block Headers (Skip blocks)
    // 3. 加载新 Block 并搜索
}
```

#### C. RoaringIterator (新特性)
基于 `roaring64.Bitmap` 的迭代器。利用 Roaring 的高效位运算能力。

```go
import "github.com/RoaringBitmap/roaring/roaring64"

type RoaringIterator struct {
    key    QKey
    iter   *roaring64.IntIterable64 // 或使用 Iterator
    curEID EntryID
}

func NewRoaringIterator(key QKey, bitmap *roaring64.Bitmap) *RoaringIterator {
    it := &RoaringIterator{
        key:  key,
        iter: bitmap.Iterator(),
    }
    if it.iter.HasNext() {
        it.curEID = EntryID(it.iter.Next())
    } else {
        it.curEID = NULLENTRY
    }
    return it
}

func (it *RoaringIterator) SkipTo(target EntryID) EntryID {
    if it.curEID >= target {
        return it.curEID
    }
    // RoaringBitmap 的 Iterator 是顺序的
    // 对于稀疏数据，线性 Next() 通常足够快
    // 对于极其稠密的数据，Roaring 内部有优化
    // 如果需要更快的 Skip，可能需要使用 AdvanceIfNeeded (如果库支持) 
    // 或者自行基于 Rank/Select 实现 (但在迭代器模式下较复杂)
    
    // 简单实现：线性推进
    for it.curEID < target && it.curEID != NULLENTRY {
        if it.iter.HasNext() {
            it.curEID = EntryID(it.iter.Next())
        } else {
            it.curEID = NULLENTRY
        }
    }
    return it.curEID
}
```

*优化注*: `roaring64` 库目前的迭代器不支持 `Advance`。如果性能瓶颈明显，可以考虑：
1.  如果 Bitmap 非常稀疏，线性 `Next` 是可接受的。
2.  如果需要跳跃，可以利用 `bitmap.Rank(target)` 快速定位，但这需要每次从头计算，对于 Iterator 模式不一定比线性快。
3.  最理想的方式是向 Roaring 社区贡献 `Advance` 接口，或者使用 `ToArray` 转为 SliceIterator (权衡内存 vs 速度)。考虑到我们已经使用了 `Segment Tree` 将大区间拆分，单个 Bitmap 可能不会特别大，`ToArray` 转换为 `SliceIterator` 也许是一个务实的短期方案，或者直接使用线性迭代。

## 4. 实施步骤

1.  **重构 `index_scanner.go`**:
    *   定义 `SliceIterator` 结构体及方法。
    *   定义 `BlockIterator` 结构体及方法。
    *   保留 `EntriesCursor` 作为接口兼容层（可选，或直接替换）。
    *   实现 `RoaringIterator`。

2.  **更新构造函数**:
    *   `NewEntriesCursor` -> 返回 `*SliceIterator` (作为 `TermIterator` 接口)。
    *   `NewEntriesCursorForCompressed` -> 返回 `*BlockIterator`。
    *   新增 `NewRoaringIterator`。

3.  **适配现有代码**:
    *   检查 `entries_holder_default.go` 等文件，将对 `EntriesCursor` 的具体类型依赖改为接口依赖，或更新构造调用。
    *   特别注意 `GetEntries` 方法的返回值类型。

4.  **性能测试**:
    *   Benchmark 对比新旧实现的内存占用和 `SkipTo` 性能。

## 5. 预期收益

*   **内存减少**: `SliceIterator` 不再持有 1KB 的 `decompBuffer`。
*   **CPU 效率**: 移除 `SkipTo` 热点路径的分支判断。
*   **代码清晰**: 各自职责单一，便于后续维护和针对性优化（如为 SliceIterator 引入 SIMD 查找）。
*   **功能扩展**: 原生支持 RoaringBitmap，为 `OptimizedRangeHolder` 的进一步优化打下基础。
