# be_indexer 完整设计、架构与性能评估

## 1. 总体结论

`be_indexer` 的核心算法设计是优秀且成立的：通过 `EntryID` 高位编码 K、统一有序 `PostingIterator`、单次 K-Groups 归并，实现了无需物理 K 分桶的布尔表达式索引；同时配合 mmap 段、full+delta 快照和可插拔容器，整体架构已经具备生产级雏形。

但当前实现还不能完全满足文档声明的三个强约束：

- **Retrieve 零分配**：端到端查询仍有较多分配。
- **构建内存严格有界**：spill 后最终归并仍会整体回灌内存。
- **跨平台端序安全**：部分 `unsafe.Slice` 直接按本机端序解释小端数据。

总体判断是：**算法主干成熟，模块化设计合理；正确性防线、资源上界和工程声明需要再收紧一轮。**

---

## 2. 架构分析

### 2.1 核心链路

#### 2.1.1 Document 编译

```text
Document
  └── Conjunction
       └── Predicate
            └── PredicateEncoder
                 └── EncodedPosting
```

- 每个 Conjunction 生成一个 `ConjID`。
- 每个物理索引记录生成一个 `EntryID`。
- K 表示 Conjunction 中 Include Field 的数量，编码于 `EntryID` bits 56–63。
- 主入口位于 `builder/doc_exporter.go`。

`ConjID` 的布局为：

```text
[ reserved(4) | K(8) | conjunction index(8) | sign(1) | DocID(43) ]
```

`EntryID` 的布局为：

```text
[ ConjID(60) | reserved(3) | include/exclude(1) ]
```

K 被移动到 `EntryID` 的最高 8 位。因此，直接按 `EntryID` 排序即可自然形成 K 分组，不需要额外维护 K bucket。

#### 2.1.2 索引构建

构建分为两条路径：

- 小规模构建使用 `InMemorySegmentBuilder`。
- full build 使用 `ExternalBuilder`、分段和临时 run。

每个 Field 由独立的 `IndexBuilder` 负责。框架只处理：

- Field 与 IndexBuilder 的装配；
- block 对齐；
- block checksum；
- metadata；
- Segment footer；
- wildcard block。

容器扩展接口位于 `segment/field_index.go`：

```go
type IndexBuilder interface {
    AddRecord(record any, entries []core.EntryID) error
    Build(bw BlockWriter) error
}

type IndexReader interface {
    MatchQuery(
        ctx BlockContext,
        field core.BEField,
        query interface{},
    ) ([]core.PostingIterator, error)
}
```

该抽象使 Engine 不需要了解 FlatDict、MPH、FST、AC 或 RangeIndex 的物理结构。

#### 2.1.3 物理段布局

```text
Magic "BEIDX\0\0\4"
├── Field A blocks, 8-byte aligned
├── Field B blocks, 8-byte aligned
├── ...
├── __wildcards
├── MetaBlock JSON
└── MetaOffset uint64 LE
```

当前各容器实际布局：

| IndexType | 主要物理布局 |
|---|---|
| `default` | Posting block + FlatDict |
| `mph_dict` | Posting block + FlatDict + CHD |
| `fst_dict` | Posting block + vellum FST |
| `ac_matcher` | Posting block + FlatDict + DAT automaton |
| `ext_range` | Point array + segment tree + 内部 posting region |

Posting block 和 wildcard block 按 8 字节对齐，以便将 mmap 数据映射为 `[]EntryID`。

#### 2.1.4 在线检索

检索链路为：

```text
Assignments
  └── PredicateEncoder.Query
       └── EncodedQuery
            └── SegmentReader.IndexQuery
                 └── PostingIterator
                      └── FieldCursor
                           └── mergeCursors
```

每个 Field 会收集：

- 所有 Segment；
- 所有查询值；
- 所有命中容器节点；

对应的 `PostingIterator`。

Field 内通过小根堆做懒归并，Field 间通过排序后的 `FieldCursors` 执行 K-Groups。

检索主算法位于 `engine/searcher.go`：

```text
eid = 当前最小 EntryID
K = eid.ConjID.Size()
need = max(K, 1)

若前 need 个 FieldCursor 指向同一个 ConjID：
    include -> collect DocID
    exclude -> 将其余 cursor 跳过当前 Conjunction

推进前 need 个 cursor
移除已耗尽 cursor
继续循环
```

该算法依赖一个极其重要的不变量：

> 所有容器返回的 PostingIterator 都必须严格按 `EntryID` 非递减顺序输出。

任何按业务值、区间下界或节点 ID 排序，而不是按 `EntryID` 排序的容器，都会破坏 `SkipTo` 和 K-Groups 的正确性。

#### 2.1.5 增量与发布

最终查询语义为：

```text
Result =
    (FullResult - ChangedDocs)
    ∪
    (DeltaResults - DeletedDocs)
```

多个 delta 通过后继 `changed_docs` 屏蔽旧版本，能够覆盖：

- update；
- delete；
- delete then recreate；
- 多 delta 中后版本覆盖前版本。

Manifest 和 CURRENT 采用临时文件、`fsync`、rename 的方式原子发布。

Holder 使用 `atomic.Value` 切换不可变 `CompositeEngine` 快照。新快照只有在 Manifest、Segment 和 Sidecar 加载成功后才发布。

---

## 3. 设计优点

### 3.1 K 编码消除了物理分桶

将 K 编入 `EntryID` 高位是整个设计中最关键的优化之一。它减少了：

- 构建时的 K bucket；
- 每个 Field 的重复 Dictionary；
- 查询时的 per-K 初始化；
- Segment metadata；
- Engine 分支。

### 3.2 统一迭代器契约合理

所有容器只需输出按 `EntryID` 升序的 Iterator。Engine 不感知：

- FlatDict；
- MPH；
- FST；
- AC automaton；
- Segment Tree；
- Geo Encoder。

这使得检索算法和物理索引容器解耦。

### 3.3 Encoder 与 IndexType 分离

`FieldOption.Encoder` 负责将业务值转换为物理查询值，`FieldOption.IndexType` 负责决定如何存储和查询这些值。

这种设计允许：

- `number` encoder + `default`；
- `default` encoder + `mph_dict`；
- `default` encoder + `fst_dict`；
- `proximitygeo` encoder + `default`；
- `ext_range` encoder + `ext_range`。

该方向比让每个容器自行理解业务值更清晰。

### 3.4 范围索引方向正确

当前 `ext_range` 使用混合布局：

- `Lo == Hi` 进入 packed point array；
- `Lo < Hi` 进入 segment tree。

这样避免等值约束污染 segment tree 的 endpoint 集合。

此前分支的 point-heavy 基准中，在 100K Predicate、90% equality 的场景下：

- 文件体积约下降 3.1 倍；
- tree node 数约下降 6.6 倍。

这些精确数字是此前分支基准，本次没有重新测量，但优化方向仍然成立。

### 3.5 full+delta 语义完整

当前实现不仅仅是将 full 和 delta 结果做 OR，还正确处理：

- full 中的旧版本；
- delta update 后不再命中的 Document；
- delete；
- recreate；
- 多 delta 的版本覆盖。

### 3.6 测试体系较好

已有测试覆盖：

- oracle shadow；
- range randomized shadow；
- range differential fuzz；
- Segment corruption；
- checksum mismatch；
- multi-delta update；
- concurrent reload；
- race detector；
- FST/MPH 并发查询；
- mmap 打开。

---

## 4. 关键问题

## 4.1 P0：正确性与契约

### 4.1.1 Schema/IndexType 校验不完整

`SchemaCodec` 当前主要验证 Encoder，没有统一验证 `IndexType` 是否已注册。

与此同时，构建路径调用 `AddField` 后存在错误被忽略的情况：

- `builder/doc_exporter.go`
- `builder/artifact.go`

这会导致：

- Schema 编译成功；
- 实际 Field container 初始化失败；
- 后续构建或查询出现更隐蔽的问题。

此外，`fst_dict` 和 `mph_dict` 依赖包级 `init()` 注册，但文档没有明确要求应用做 side-effect import。

建议：

1. Schema 编译时同时验证 Encoder 和 IndexType。
2. 所有 `AddField` 错误必须向上返回。
3. 提供统一 built-in 注册包，或在文档中明确：

```go
import (
    _ "github.com/echoface/be_indexer/container/fst"
    _ "github.com/echoface/be_indexer/container/mph"
)
```

### 4.1.2 未配置的 Document Field 被静默忽略

构建时，若 Document 包含 Schema 中不存在的 Field，当前逻辑会直接跳过。

但 K 已经在跳过前根据 Document 逻辑计算：

- 未配置 Include 会让 K 仍然包含该 Field，但索引中没有对应 posting，导致 Conjunction 永远无法凑齐，产生假阴性。
- 未配置 Exclude 会被完全忽略，产生假阳性。

因此，默认行为应是构建失败。

若业务确实需要容忍未索引 Field，应提供显式配置：

```text
IgnoreUnindexedFields = true
```

并在计算 K 前进行逻辑归一化。

### 4.1.3 查询错误被静默吞掉

当前查询有两类错误会被当作“没有命中”：

- Encoder.Query 返回错误；
- SegmentReader.IndexQuery 返回错误。

这意味着以下问题可能退化为错误结果，而不是显式失败：

- 查询值类型错误；
- posting offset 损坏；
- 自定义容器错误；
- Range/AC block 异常。

建议：

- 默认 strict mode：立即返回错误；
- 若业务需要容错，提供明确的 lenient mode；
- Observer 中区分 no-match 与 skipped-error。

### 4.1.4 `FailSkip` 不是 Document 级事务

`exportDocToSink` 当前边编码边写入。

如果一个 Document 的前几个 Predicate 已经写入，后续 Predicate 才编码失败，`FailSkip` 只会记录跳过，但不会回滚先前已写入的数据。

因此可能留下“半个 Document”。

正确做法是：

```text
Document
  └── 完整验证
       └── 编码到 document-local buffer
            └── 全部成功后一次性提交给 sink
```

或者让 sink 支持：

```text
BeginDocument
CommitDocument
RollbackDocument
```

预编码后原子提交更简单。

### 4.1.5 Full build 没有强制 DocID 唯一

`ConjID` 只由以下字段组成：

- DocID；
- Conjunction Index；
- K。

如果 full build 中出现重复 DocID，且两份 Document 的 Conjunction Index 和 K 相同，则它们会生成相同 `ConjID`。

不同 Document 版本的 Predicate 可能被拼接成一个原本不存在的 Conjunction。

建议：

- full build 拒绝重复 DocID；
- 或者先按 DocID/version 压实；
- 对 push builder 可使用磁盘辅助去重或要求输入按 DocID 排序。

### 4.1.6 同一 Field 多个 Constraint 的语义不明确

当前 API 允许同一 Field 添加多个 `ValueExpr`，而 K 只按 Field 计数。

FieldCursor 又会将同一 Field 下多个 posting 合成 OR。

因此，以下逻辑存在歧义：

```text
age > 18 AND age < 60
```

如果两个 ValueExpr 都是 Include：

- 布尔直觉通常是 AND；
- 当前 K 只要求该 Field 命中一次；
- 实际执行容易成为 OR。

建议明确规范化规则：

- 一个 Field 最多一个 Include Constraint；
- 多个值放在一个 Constraint 内，值之间为 OR；
- 可以有多个 Exclude，任意一个命中即 veto；
- 对需要区间交集的场景，在构建前合并为单个标准区间；
- 无法归一化的表达式直接拒绝。

---

## 4.2 P1：构建与存储

### 4.2.1 ExternalBuilder 还不是严格内存有界

`MaxPostingsInMemory` 当前实际限制的是 distinct key 数，不是真实 posting 数。

单个热点 key 的 `Entries` 可以持续增长而不触发 spill。

更重要的是，`KeyedPostingCollector.Merge()` 最终仍返回完整的：

```go
[]KeyedRecord
```

之后构建器还会创建：

- 所有 Posting block bytes；
- FlatDict/FST/MPH/AC block；
- RangeIndex 中间结构；
- container-specific 临时对象。

因此 peak memory 可能接近：

```text
merged records
+ all EntryIDs
+ posting block
+ dictionary/index block
+ container-specific temporary structures
```

建议：

1. run 格式按 `(key, EntryID)` 排序。
2. spill 阈值按 Entry 数和估算字节数，而不是 distinct key 数。
3. 最终 merge 返回流式 iterator，而不是完整 slice。
4. Posting writer 边 merge 边写。
5. Dictionary/FST/MPH 仅保留必要的 key→offset 信息。
6. BlockWriter 支持流式写入、checksum 和 header 回填。

### 4.2.2 Wildcard sidecar 最终仍被 `io.ReadAll`

ExternalBuilder 写 Segment 时会把 wildcard sidecar 完整读回内存。

这削弱了 wildcard spill 的意义。

建议允许：

```go
WriteBlockFromReader(kind string, size uint64, reader io.Reader)
```

从文件直接复制到 Segment，并在复制过程中计算 checksum。

### 4.2.3 部分 Reader 并非完全零拷贝

当前真正零拷贝的是主要 Posting EntryID 数据。

但：

- FlatDict 打开时复制所有 `dictItem`；
- RangeReader 复制 node、point key 和 offset 数组；
- ACReader 构建 rune→symbol map。

因此，“posting zero-copy”成立，但“整个 SegmentReader zero-copy”不成立。

以 FlatDict 为例，每个词条 metadata 为 16 字节。百万词表单个 Field、单个 Segment，仅 `dictItem` 就约 16 MB。

建议将文档表述改为：

> Posting data and selected index blocks are mmap-backed; small lookup metadata may be decoded into heap structures.

如果要进一步降低 heap：

- FlatDict 直接在 byte block 上读取 item；
- RangeIndex 使用 mmap view 或结构化 SoA；
- 对常用 metadata 使用定长、小端读取函数。

### 4.2.4 端序约束没有落实

文件格式使用 Little Endian，但部分读取通过 `unsafe.Slice` 直接将 bytes 解释为整数数组。

这只在 Little Endian 主机上正确。

在 Big Endian 主机上：

- Posting EntryID；
- AC base/check/fail/output arrays；

都会被错误解释。

短期建议：

- 加启动期端序检测；
- Big Endian 平台拒绝 mmap zero-copy；
- Big Endian 使用显式 LittleEndian 解码复制。

长期建议在格式文档中明确：

```text
All numeric fields are little-endian.
Zero-copy integer views are only enabled on little-endian hosts.
```

### 4.2.5 隐藏的 uint32/uint16 上限缺少统一校验

当前格式中还有多处隐含上限：

- FlatDict key offset：uint32；
- Posting count：uint32；
- Range posting offset：uint32；
- AC state/output pointer：uint32；
- AC 单状态 output count：uint16；
- SegmentReader dense field ID：uint16。

如果数据超过对应上限，可能产生：

- 截断；
- 越界；
- 错误引用；
- 构建期 panic；
- 查询期错误。

建议：

- 构建前统一检查格式上限；
- `WriteFlatPostingList` 返回 error，而不是 panic；
- Segment rolling 同时按 docs、posting 数和字节数触发；
- 对单 block 4 GiB 上限给出明确错误。

### 4.2.6 mmap 默认完整性策略偏弱

`UseMmap=true` 且未开启 block checksum 验证时，Loader 只验证 checksum metadata 存在，不计算实际 checksum。

这是为了避免打开时 fault-in 全部页面，但需要清晰区分信任模型。

建议定义两个 profile：

```text
TrustedLocalFast
    - mmap
    - 验证格式和 block bounds
    - 不扫描全部 block

StrictIntegrity
    - 验证 whole-file checksum 或全部 block checksum
    - 适合远端分发、首次下载、灾备恢复
```

生产流程可以先在独立 staging 目录完成严格校验，再原子切换 CURRENT。

---

## 4.3 P1：生命周期

### 4.3.1 Reload 后旧 mmap 依赖 GC finalizer 回收

Holder reload 后不会主动关闭旧快照，因为在途查询可能仍持有旧 Segment 的 zero-copy view。

这是正确的安全考虑，但当前旧快照主要依赖 GC finalizer 回收 mmap。

高频 reload 下可能积累：

- mmap 映射；
- 文件描述符；
- 页表；
- FST reader pool；
- decoded metadata。

建议使用 RCU/refcount：

```text
Query:
    snapshot.Acquire()
    defer snapshot.Release()

Reload:
    publish new snapshot
    old snapshot -> retire queue

Retire:
    refcount == 0
    Close/unmap old snapshot
```

Finalizer 只作为兜底，不作为正常资源管理路径。

### 4.3.2 Loader 失败路径应统一回收已加载资源

如果 full 已成功打开，但后续 delta 或 sidecar 加载失败，已打开的 SegmentReader 应立即关闭。

建议：

- `LoadSnapshot` 使用资源 owner；
- 成功后 transfer ownership；
- 任意错误路径统一 defer cleanup。

---

## 5. 性能评估

### 5.1 本次定向基准

测试环境：

- Apple M3 Pro；
- darwin/arm64；
- Go 1.24；
- 当前分支代码。

| 场景 | 延迟 | 分配 |
|---|---:|---:|
| High-K Retrieve | 12.6–12.8 µs | 7.5 KB，255 allocs |
| Composite full-only | 6.4–6.7 µs | 5.7 KB，184 allocs |
| Composite full+1 delta | 8.3–8.5 µs | 7.5 KB，248 allocs |
| FlatDict 1M random Find | 243–269 ns | 0 alloc |
| MPH 1M hit MatchQuery | 86–90 ns | 128 B，5 allocs |
| FST 1M hit MatchQuery | 248–250 ns | 128 B，5 allocs |
| Range 100K intervals | 459–529 ns | 381 B，13 allocs |

这些结果说明：

1. FlatDict 底层查找已经很快，继续微调二分收益有限。
2. 主要分配来自查询结果传递和游标装配，不是字典查找本身。
3. `Retrieve()` 为返回独立 bitmap 必须 clone，天然难以做到零分配。
4. 真正的零分配目标应放在新的 `RetrieveInto` 或 `RetrieveWithWorkspace` API。

### 5.2 查询分配来源

主要分配包括：

- Encoder 输出 `[]EncodedQuery`；
- 每个容器返回的 `[]PostingIterator`；
- `FlatPostingList`；
- `flatPostingCursor`；
- `FieldCursor.Iters`；
- wildcard iterator slice；
- outer `FieldCursors`；
- RoaringBitmap clone；
- Composite 中每个 delta 的独立结果 bitmap。

### 5.3 查询时间复杂度

设：

- `S`：Segment 数；
- `F`：有效 Assignment Field 数；
- `Qf`：Field 编码出的查询值数；
- `If`：该 Field 最终产生的 Iterator 数；
- `D`：Delta 数。

初始化复杂度约为：

```text
Σ(S × Qf × container lookup)
```

Field 内归并：

```text
每次推进 O(log If)
```

Posting 内：

```text
顺序推进接近 O(1)
大跳跃约 O(log distance)
```

Field 间当前每轮重新排序：

```text
O(F log F)
```

Composite：

```text
(D + 1) 次完整检索
+ Roaring bitmap AndNot/Or
```

实际性能主要受以下因素控制：

- 高频词的 Posting 长度；
- 查询 Field 数；
- 每个 Field 的多值数量；
- Segment 数；
- Delta 数；
- Range/AC 一次查询返回的 Iterator 数；
- 冷启动 page fault；
- Result cardinality。

### 5.4 容器评估与选择

#### default

适合：

- 小中型词表；
- 普通精确匹配；
- 构建速度优先；
- 无额外依赖。

当前 FlatDict 在百万词表下 random Find 约为 250 ns，已经足够优秀。

#### mph_dict

适合：

- 高熵大词表；
- ID、hash、opaque token；
- 精确匹配；
- 不需要枚举和前缀能力。

当前实测 hit MatchQuery 约为 86–90 ns。

但当前构建仍通过 `DictPostingsWriter` 写 FlatDict，再额外写 CHD。这抵消了 MPH“不保留 term dictionary”的空间优势。

建议 MPH 使用专用 postings writer，只写：

```text
Posting block + CHD
```

#### fst_dict

适合：

- URL；
- 路径；
- package/class name；
- 自然语言 token；
- 共享前缀或后缀较多的大词表。

查询时间约为 `O(len(term))`，基本不随词表大小增长。

当前池化 `*vellum.Reader` 的方向正确，但完整 `MatchQuery` hit 仍会因 iterator 包装产生分配。

#### ac_matcher

适合：

- 文本中同时匹配大量固定模式；
- 需要 `O(len(text))` 扫描。

风险在于：

- 命中模式多时产生大量 Iterator；
- output payload 使用 uint16 count；
- Query text 可能命中重复 pattern output；
- iterator fanout 可能成为 K-Groups 前的主要成本。

#### ext_range

当前混合 point + segment tree 的设计正确。

查询复杂度大致为：

```text
point lookup: O(log P)
tree descent: O(log M)
iterator count: O(tree depth)
```

当前主要优化空间不再是 point binary search，而是：

- iterator slice 分配；
- `FlatPostingList` 包装；
- RangeReader metadata copy；
- 构建阶段中间结构内存。

#### proximitygeo

当前实现是 geohash covering candidate match，不是精确球面距离判断。

它适合：

- 容忍 geohash cell 误差；
- 需要高性能粗筛；
- 不要求严格 radius boundary。

文档应明确：

- false positive 边界；
- 是否允许 false negative；
- 不等同于 Haversine 精确匹配。

---

## 6. 优化路线

## 6.1 第一阶段：先修正确性

建议优先完成：

1. 增加 `ValidateAndNormalizeDocument`。
2. 强制 DocID 唯一或预压实。
3. 拒绝 nil Conjunction 和 nil Constraint。
4. 明确同一 Field 多 Constraint 的语义。
5. Schema 同时验证 Encoder、IndexType 和注册状态。
6. 所有 `AddField` 错误向上返回。
7. Encoder.Query 和 IndexQuery 错误不再静默吞掉。
8. 将 `FailSkip` 改为预编码后原子提交。
9. 增加损坏自定义容器 fuzz，保证 Retrieve 不 panic。

这一阶段对线上正确性的收益最高。

## 6.2 第二阶段：实现真正 bounded-memory build

建议采用最小接口改造：

```go
type KeyedRecordIterator interface {
    Next() bool
    Key() []byte
    Entry() core.EntryID
    Err() error
    Close() error
}
```

构建流程改为：

```text
AddRecord
  └── append (key, EntryID)
       └── threshold by bytes/entries
            └── sorted run

Build
  └── k-way streaming merge
       ├── streaming Posting writer
       └── streaming Dictionary/Container builder
```

具体步骤：

1. Collector 阈值改为 Entry 数和内存字节数。
2. run 按 `(key, EntryID)` 排序。
3. 最终 merge 输出流，不返回完整 slice。
4. BlockWriter 支持流式 block、checksum 和 offset 回填。
5. Dict/FST/MPH/AC 共享 streaming postings writer。
6. Range tree 构建增加独立内存预算或二阶段临时文件。
7. Segment rolling 同时考虑：
   - docs；
   - postings；
   - bytes；
   - 单 block 4 GiB 上限。

## 6.3 第三阶段：优化查询分配

建议将：

```go
MatchQuery(...) ([]PostingIterator, error)
```

改为：

```go
MatchQuery(
    ctx BlockContext,
    field core.BEField,
    query any,
    dst IteratorSink,
) error
```

或者：

```go
AppendQueryIters(dst []PostingIterator, ...) ([]PostingIterator, error)
```

再引入池化 `QueryWorkspace`：

```go
type QueryWorkspace struct {
    EncodedQueries []encodedField
    Iterators      []core.PostingIterator
    FieldCursors   []core.FieldCursor
    HeapStorage    []core.PostingIterator
}
```

建议提供两级 API：

```go
Retrieve(...)
    // 便利 API，返回 owned bitmap，允许分配

RetrieveInto(workspace, collector, ...)
    // 高性能 API，追求 steady-state 低分配或零分配
```

进一步优化：

- 移除热路径仅用于调试的 `Term.Value any`；
- 数字 Encoder 使用 `strconv.AppendInt` 和 workspace buffer；
- Range/AC 直接向 caller workspace 追加 Iterator；
- 对 wildcard Iterator 使用预分配数组；
- Composite 允许 caller 提供 bitmap accumulator。

在完成 allocation 优化后，再重新 benchmark：

- outer FieldCursor sort；
- outer heap；
- 局部有序修复；

不要在对象分配仍占主导时过早微调排序算法。

## 6.4 第四阶段：生命周期与格式治理

建议：

1. 实现带引用计数的 snapshot retire。
2. 自动生成 canonical schema hash，至少包含：
   - Field name；
   - Field ID；
   - IndexType；
   - Encoder；
   - 容器格式版本；
   - 容器参数。
3. 统一 Manifest `segment-v2` 与实际 Segment v4 的命名。
4. 增加 golden segment 兼容性测试。
5. 增加 Big Endian decode 测试。
6. 增加高频 reload soak test。
7. 修正文档中的过强表述：
   - “Retrieve 零分配”；
   - “SegmentReader 完整零拷贝”；
   - “ExternalBuilder 严格 bounded memory”。

---

## 7. 建议的性能验收指标

### 7.1 Query

按场景建立固定 benchmark matrix：

```text
K:                0 / 1 / 4 / 8 / 32
Fields/query:     1 / 4 / 16 / 64
Segments:         1 / 4 / 16
Deltas:           0 / 1 / 4 / 16
Posting length:   10 / 1K / 100K / 1M
Result count:     0 / 10 / 10K / 1M
Container:        default / mph / fst / ac / range
```

至少记录：

- ns/op；
- B/op；
- allocs/op；
- p50/p95/p99；
- page faults；
- RSS；
- CPU cycles；
- branch misses。

### 7.2 Build

按以下维度测试：

```text
Documents:          1M / 5M / 20M
Predicates/doc:     5 / 10 / 50
Distinct terms:     10K / 1M / 20M
Hot-key ratio:      0% / 50% / 99%
Range equality:     0% / 50% / 90%
MaxPostingsMemory:  100K / 1M / 10M
```

记录：

- wall time；
- CPU time；
- peak RSS；
- total allocations；
- GC pause；
- spill bytes；
- run count；
- merge fan-in；
- temporary disk peak；
- final Segment size。

### 7.3 Reload

建立 soak test：

```text
查询并发: 100+
reload 周期: 1s / 10s
持续时间: 1h+
```

观测：

- mmap count；
- file descriptor count；
- RSS；
- old generation retire latency；
- reload failure recovery；
- in-flight query correctness。

---

## 8. 验证结果

本次审查执行了：

- `go test ./...`
- `go test -race ./core ./engine ./segment ./builder ./loader ./container/fst ./container/mph`
- `go vet ./...`
- 定向 benchmark：
  - Engine High-K；
  - Composite full/full+delta；
  - FlatDict；
  - RangeIndex；
  - FST；
  - MPH。

结果：

- 所有 package 测试通过。
- race detector 通过。
- vet 未报告代码问题。
- 命令最终退出码为 1，仅因为沙箱禁止 Go 清理用户目录下的 `go-build/trim.txt`，不是测试、race 或 vet 失败。

---

## 9. 最终建议

当前不建议首先继续优化某个容器的纳秒级查找，因为底层查找已经足够快。

推荐实施顺序：

1. **正确性**
   - Schema/Document 严格校验；
   - 错误传播；
   - `FailSkip` 事务化；
   - DocID 唯一性；
   - Constraint 语义规范化。

2. **构建资源上界**
   - streaming merge；
   - streaming block writer；
   - 按 posting/bytes 控制 spill；
   - 消除最终全量 materialization。

3. **查询对象模型**
   - Iterator sink；
   - QueryWorkspace；
   - `RetrieveInto`；
   - 降低 cursor 和结果 bitmap 分配。

4. **生命周期和格式**
   - snapshot refcount/RCU；
   - canonical schema hash；
   - 端序处理；
   - 格式版本治理；
   - 完整性 profile。

完成以上工作后，`be_indexer` 才能从“算法和架构优秀的高性能索引库”进一步提升为“在正确性、资源上界、热更新和性能声明上都可严格验证的生产级索引内核”。
