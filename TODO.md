# be_indexer 架构演进路线图 (TODO)

本项目旨在将 be_indexer 从全内存单体原型演进为支持大规模数据、增量更新和低内存占用的工业级检索引擎。

## Phase 1: 架构分层 - Builder/Searcher 分离
目标：解决 Holder 职责不清的问题，将写入（构建）与读取（检索）逻辑物理分离。

- [x] **设计接口定义** (`static/design/refactor_phase1_builder_searcher_split.md`)
    - 定义 `SegmentBuilder` 接口：负责数据积累、去重、暂存。
    - 定义 `SegmentReader` 接口：负责不可变数据的查询。
    - 定义 `Segment` 结构：作为数据交换的标准单元。
- [x] **重构 DefaultKVHolder**
    - 拆分为 `MemSegmentBuilder` (基于 Map) 和 `BlockSegmentReader` (基于 FlatArray)。
    - 实现 `Compile()` (Flush) 方法：将 Builder 数据序列化为 Reader 可用的二进制格式。
- [x] **适配现有 Indexer**
    - 修改 `BEIndex` 以支持新的 Segment 接口 (GetSegment)。

## Phase 2: 核心数据结构升级 - Term Dictionary 与 TermID
目标：解决字符串存储带来的内存膨胀和 GC 压力，引入 TermID 映射机制。

- [x] **设计 Term Dictionary** (`static/design/refactor_phase2_term_dict_fst.md`)
    - 引入 `TermDictionary` 抽象。
    - 方案 A (MVP): 有序字符串数组 + 二分查找。
    - 方案 B (进阶): FST (Finite State Transducer) 实现极低内存占用。
- [x] **倒排链改造**
    - 倒排链 Key 从 `string` 迁移为 `uint64 (TermID)`。
    - 存储结构改为 `TermID -> Offset -> CompressedPostingList`。
- [x] **重构 Index Scanner**
    - 查询流程变更：`Query(String)` -> `Dict.Get(String) -> TermID` -> `Postings.Get(TermID)`。

## Phase 3: 规模化与增量 - Segment 架构与合并
目标：支持亿级数据规模，支持近实时 (NRT) 增量更新。

- [ ] **设计 Segment 管理器** (`static/design/refactor_phase3_segment_architecture.md`)
    - 实现 `SegmentManager`：管理多个不可变的 Segment。
    - 实现 `MultiSegmentIndex`：聚合查询逻辑。
- [ ] **实现增量更新**
    - 引入 `MutableSegment` (内存表) 处理实时写入。
    - 引入 `BitSet` (Tombstones) 处理删除。
- [ ] **实现 Compaction (合并)**
    - 后台合并策略：将多个小 Segment 合并为大 Segment，物理删除无效数据。

## Phase 4: 大规模构建与离线分发 (Large Scale Building)
目标：支持 500w-1000w+ 规模文档的构建，解决单机内存瓶颈，实现 Map-Reduce 风格的构建流程。

### 4.1 整体架构：Disk-Based K-Way Merge Sort
放弃全量内存构建，采用 **分治 + 流式合并** 策略。

1.  **Map 阶段 (Ingestion & Spill)**:
    *   流式读取文档，使用 `MemSegmentBuilder` 在内存积累。
    *   达到阈值（如 50w 文档或 1GB 内存）时，对当前数据进行排序并 Flush 到磁盘，生成临时文件 (`RunFile`)。
    *   `RunFile` 格式应为简单的流式 KV 对，便于后续归并。
2.  **Reduce 阶段 (K-Way Merge)**:
    *   打开所有 `RunFile`，使用优先队列 (Min-Heap) 进行多路归并。
    *   输出严格有序的 `(Term, DocIDList)` 流，直接喂给 `vellum.Builder` (FST) 和 `PostingsWriter`。
    *   内存占用仅与 `RunFile` 数量相关 (O(K))，与数据总量无关。

### 4.2 各 Holder 的适配策略

#### A. DefaultKVHolder (Term Dictionary + Postings)
*   **构建**: 完全兼容上述 Merge Sort 流程。
*   **FST**: `vellum` 要求 Key 有序输入，多路归并天然满足此要求，可实现流式构建全局 FST。

#### B. ACMatcher (Aho-Corasick)
*   **现状**: 当前实现使用 `github.com/anknown/ahocorasick` (基于 Double-Array Trie, DAT)。
*   **挑战**: DAT 构建通常需要全量 Keyword 在内存中。若 Unique Keyword 数量极大 (如 >1000w)，全局构建会 OOM。
*   **策略**:
    1.  **推荐方案 (Segment-local)**: 保持 Segment 架构，每个 Segment (50w 文档) 拥有独立的 DAT。检索时并发查询所有 Segment 的 AC 自动机。这是最稳健且易于并行的方案。
    2.  **备选方案 (Global DAT)**: 在 Merge 阶段流式收集所有 Unique Keyword。若总数在内存可承受范围内 (如 <500w)，则构建全局 DAT；否则退回 Segment-local 方案。

#### C. OptimizedRangeHolder (Range Query)
*   **现状**: 使用 Bitset (全量文档) + 离散化区间树。
*   **挑战**: 需要全局文档空间信息。
*   **策略**:
    1.  **推荐方案 (Segment-local)**: 每个 Segment 维护自己的 Range 索引和局部 Bitset。
    2.  **合并方案**: 若需全局合并，需在 Merge 阶段重新扫描所有 Segment 的区间边界，重新进行全局离散化并重构区间树。

### 4.3 离线分发
*   构建产物应为标准化的 **Segment 文件** (包含 FST Dict, Postings, DAT 等)。
*   支持将 Segment 文件分发到检索节点，节点通过 `Load()` 接口直接映射内存 (mmap) 或加载，实现快速冷启动。

## Phase 5: 增量更新与实时性 (Incremental & Real-time Updates)
目标：支持广告的部分更新、新增和删除，采用 LSM-Tree (Log-Structured Merge-Tree) 思想，复用 Phase 4 的分段与合并机制。

### 5.1 核心机制：LSM-Tree 范式
对于“一次仅更新部分广告”的场景，利用分段架构实现**写时不可变 (Immutable)** + **读时合并 (Merge on Read)**。

1.  **新增/修改 (Write)**:
    *   不修改存量大 Segment。
    *   在内存构建微型 `MemSegment`（如 1000 条更新）。
    *   快速 Flush 为独立的微型 Segment (Mini-Segment)。
2.  **删除/覆盖 (Delete/Update)**:
    *   引入 **BitSet (LiveDocs)** 或 **Tombstones** 机制。
    *   更新 = 删除旧版本 + 新增新版本。
    *   在查询时过滤掉被标记为删除的文档 ID。
3.  **后台合并 (Compaction)**:
    *   复用 Phase 4 的 **K-Way Merge** 逻辑。
    *   当微型 Segment 数量达到阈值（如 10 个），触发后台归并。
    *   在归并过程中物理移除被标记删除的数据，回收空间，并合并 FST/AC/Range 结构，优化查询性能。

### 5.2 组件支持
*   **FST**: 增量时构建微型 FST；合并时流式归并为大 FST。
*   **ACMatcher**: 增量时构建微型 AC；合并时保持 Segment-local 策略或尝试全局合并。
*   **RangeHolder**: 增量时构建微型区间树；合并时重构。

此方案统一了**全量构建**（分治）与**增量更新**（LSM Compaction）的技术路径，是工业界标准解法。
