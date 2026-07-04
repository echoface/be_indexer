# Segment 架构 Review 与后续优化建议

本文记录 `segment/` 底层存储、构建与检索加载路径的 review 结论，以及第一、第二阶段之后剩余的演进建议。

## 当前已完成

### 第一阶段：命名与职责边界

- `doc.go` 拆分/重命名为 `block_name.go`，只保留 block name 约定。
- `segment_writer.go` 中的物理格式定义拆出到 `format.go`。
- wildcard / entry list block 编解码拆出到 `entry_block.go`。
- 原 `Builder` 重命名为 `InMemorySegmentBuilder`，明确其定位：适用于测试、小型 delta segment、受控 chunk，不适合大规模全量构建。
- 大规模全量构建继续由 `ExternalBuilder` 负责，通过 external sort 和 sorted run merge 控制内存峰值。

### 第二阶段：加载校验策略

- 新增 `ReaderOptions` 和 `BlockChecksumMode`。
- `NewSegmentReader` 保持严格默认行为：打开时校验所有 block checksum。
- `NewSegmentReaderWithOptions` 支持跳过打开时的全量 block checksum 扫描。
- `loader.Options.VerifySegmentBlockChecksums` 控制 serving 加载是否重复校验 block checksum。
- loader 仍通过 manifest checksum 校验完整文件；当关闭 block checksum open-time 校验时，只验证 block checksum metadata 的完整性，避免冷启动时重复 O(segment_size) re-hash。

### 第三阶段：命名扶正（`MmapReader` → `SegmentReader`）

- `MmapReader` 这个类型名把"加载方式"焊死进了类型契约，但其数据来源既可能是 mmap 也可能是普通内存，名实不符。
- 统一改名：类型 `MmapReader` → `SegmentReader`，构造 `NewMmapReader[WithOptions]` → `NewSegmentReader[WithOptions]`，相关测试函数同步。
- 精确排除了同名子串的 `ACMmapReader`（AC 自动机内存读取器，与本次无关，保持不变）。
- 全仓引用、`be_indexer.go` 顶层别名、loader/engine 全部更新；加载方式成为 `Open*` 的实现细节，不再是类型契约。

### 第四阶段：真正 mmap 化加载（已落地）

- 新增 `segment.OpenSegmentFile(path, opts)`：通过 `blevesearch/mmap-go` 以 `RDONLY` 映射文件，返回零拷贝 `SegmentReader`。
- `SegmentReader` 增加 `closer func() error` 字段，`Close()` 真正执行 `munmap`，幂等且清除 finalizer。
- **安全网**：`runtime.SetFinalizer` 在 reader 被 GC 时自动 unmap —— 忘记 `Close` 只会泄漏到回收，而不会让在途零拷贝查询 use-after-unmap 崩溃。普通 `[]byte` 路径 `closer` 为 nil，由 GC 回收。
- `loader.Options.UseMmap` 开关；`openSegmentReader` 分流 mmap vs `ReadFile`。mmap 路径**跳过整文件 checksum**（否则会 fault 全部页、抵消 mmap），完整性靠 block checksum 元数据 + 可选 `VerifySegmentBlockChecksums`，与 `BlockChecksumDisabled` 默认值天然配套。
- 生命周期：`BooleanEngine.Close` / `IndexSnapshot.Close` / `CompositeEngine.Close` / `Holder.Close` 级联释放。**关键正确性决策**：`Holder.Reload` 不主动 munmap 旧 snapshot —— 在途查询可能仍持有其零拷贝视图，旧 mmap 交由 reader finalizer 在无引用后安全回收。
- 收益：RSS 从"整文件常驻堆"降为"只计入实际触达页"；多 reader/进程共享 page cache；冷启动跳过整文件 sha256 + 按需换页；大 `[]byte` 不再进堆、降低 GC 扫描压力。

### 第五阶段：检索热路径零分配优化（批次 A，已落地）

零格式破坏的局部优化，直接服务于"检索热路径 zero-alloc"：

- **AC 查询免 `[]rune` 分配**：`ACMmapReader` 抽出共享核心 `step()`，新增 `MultiPatternSearchString(text string)`，用 `for _, r := range text` 原生 UTF-8 解码，不再分配整个 rune 数组；`SegmentReader.MultiPatternSearch` 改用 String 版。保留 `[]rune` 版兼容。公平对照 benchmark（rune 版从 string 起步）约 **-10% ns/op**。
- **blockKey 整数化**：`blockKey` 从 `struct{K int, Field string}` 改为 `uint32 = K<<16 | fieldID`（K < 256，fieldID 加载期 dense 分配）。三个检索方法统一经 `lookupBlock(k, field)` helper 查找，热路径 map key 从字符串 hash 变为整数 hash。对外签名零变化。

> 注：AC 命中仍会分配 term 字符串（当前 benchmark 的剩余 alloc 主要来源），需待"批次 B：AC output → PostingRef"才能消除。

### 第六阶段：segment v3 物理格式升级（批次 B，已落地）

一次性 bump 到 segment v3（magic `BEIDX\x00\x00\x03`），合并三项破坏格式的优化。经交叉 review 后**裁剪掉过度设计**（不做 dict zero-copy unsafe 映射、metadata 仍用 map 而非 Groups slice），以约 50% 复杂度拿约 90% 收益。

- **统一 `PostingRef{Offset, Count}` 抽象**（`posting_list.go`）：dict 与 AC output 共用的物理句柄。新增 `newPostingListAt(block, ref)` 直接按 ref 建 posting，跳过 posting 头的 count 解析。

- **项2：dict 携带 PostingRef**（`flatmap.go`）：`dictItem` 增加 `postingCount`，`WriteFlatDict` 入参改 `map[string]PostingRef`，`Find` 返回 `PostingRef`。两个 builder 写 dict 时填 `Count=len(entries)`。**按 review 建议保留 decode-copy，不上 unsafe 零拷贝**（zero-copy 收益仅在打开期一次 copy，却要承担对齐/端序/munmap 三重风险，不划算）。

- **项1：metadata 结构化 block index**（`format.go` / `segment_reader.go`）：`BlockDef` 内联 `K/Field/Kind/Checksum`，删除独立的 `BlockChecksums` map 和脆弱的 `parseBlockName` 字符串反解析。reader 改为**单次 `O(blocks)` 遍历** `BlockIndex` 直接按 `Kind` 分发。**仍保留 `map[string]BlockDef`**（`json.Marshal` 对 map 自动按 key 排序，天然保证两 builder 字节一致，规避了 Groups slice 的顺序一致性陷阱）。`Kind=wildcards` 自然容纳通配块。

- **项3：AC output 存 PostingRef**（`ac_builder.go` / `ac_mmap.go` / `segment_reader.go`）：`StaticACBuilder.Add(term, ref)`，output payload 从内联 term 字符串改为内联 `[count u16]{[offset u64][count u32]}`。reader 新增 `MatchPostingRefs(text) []PostingRef`，直接据 ref 建 cursor，**消除 AC 命中的二次 `dict.Find` 与 term 字符串分配**。`Term()` 不再携带命中词文本（检索不依赖；测试断言改为基于命中的 EntryID）。

正确性与收益：

- 两 builder（InMemory / External）字节一致性测试覆盖 v3 + dict count + AC ref，全部通过。
- AC 中文检索 benchmark：**875–968 ns/op、23 allocs/op → 454 ns/op、5 allocs/op**（ns -50%，alloc -78%）。
- 全仓 `go test -race ./...` 全绿，`go vet ./...` 无告警。

## 仍建议继续优化的方向

> 真正 mmap 化加载（原第 1 项）已在第四阶段完成；下列第 2/3/4 项已在**第六阶段（批次 B / segment v3）**全部落地，保留原始设计描述作为背景，最终实现见第六阶段小节。

### 2. metadata 结构化 block index（已完成，见第六阶段）

当前 metadata 使用字符串 block name：

```text
k{K}_{field}_dict
k{K}_{field}_postings
k{K}_{field}_ac
k{K}_{field}_range
```

reader 打开时需要遍历 `fields × blockIndex` 并 parse block name。后续建议改为结构化 metadata：

```go
type BlockGroupDef struct {
    K        int
    Field    string
    Dict     *BlockDef
    Postings *BlockDef
    AC       *BlockDef
    Range    *BlockDef
}
```

收益：

- 加载复杂度从 `O(fields * blocks)` 降为 `O(groups)`。
- 新增 block 类型不依赖字符串命名协议。
- 更接近 Lucene / Tantivy / SSTable 的 footer/index block 组织方式。

### 3. FlatDict 增加 posting count 或 posting ref（已完成，见第六阶段）

当前 `FlatDict` 只保存：

```go
term -> postingOffset
```

查询时还需要从 posting block 读取 header 才知道 count。建议升级为：

```go
type PostingRef struct {
    Offset uint64
    Count  uint32
}
```

这样 `GetPostingsByTerm` 可以直接构造 cursor，减少 header 解析和小对象分配。

### 4. AC output 直接保存 posting ref（已完成，见第六阶段）

当前 AC 查询链路是：

```text
AC match -> term string -> FlatDict.Find(term) -> posting cursor
```

建议改为：

```text
AC match -> postingRef -> posting cursor
```

构建 AC block 时，builder 已经知道 term 到 posting offset 的映射，可以把 posting offset/count 写入 AC output payload。这样可以减少 AC 查询中的二次字典查找和 string 构造。

### 5. AC 查询避免 `[]rune(text)` 分配

当前 segment reader 调用 AC 时会把 string 转成 `[]rune`。后续可以在 `ACMmapReader` 增加：

```go
func (ac *ACMmapReader) MultiPatternSearchString(text string) ...
```

内部直接 `for _, r := range text`，避免每次查询分配 rune slice。

### 6. ExternalBuilder run merge 减少 term 分配

`postingRunReader.Next` 当前每条 record 都会读取 term bytes 并转成 string。大规模构建下会带来 GC 压力。后续可考虑：

- 复用 term buffer；
- heap 中使用 byte slice view；
- run 文件按 term block/prefix-compressed stream 组织；
- 边 merge 边写 block term dictionary，避免每条记录 string 分配。

### 7. 构建期峰值内存治理（InMemory / External 全路径）

> 本节是结合"当前构建过程不管是 InMemory 还是 External 都会有大量峰值内存"问题的专项探索与调研结论，并在本库当前代码上做了交叉验证。原"Dict 数据结构演进"内容并入 7.5 作为长期可选项。

#### 7.0 结论速览（TL;DR）

| 路径 | 峰值来源 | 当前是否可控 | 建议 |
|---|---|---|---|
| InMemory | 全量 posting 的 `map[K]map[field]{Postings: map[term][]EntryID}` 常驻堆 | ❌ 不可 spill | 仅用于测试/小 delta；大规模一律走 External（已是现状契约）|
| InMemory(delta) | `bytes.Buffer` 把整段 segment 再驻留一份 | ❌ | delta 量小，可接受；超阈值切 External |
| External - record 阶段 | `records []postingRecord`（含 `Term string`）到阈值即 spill | ✅ 已受控 | 见 7.3 降低单条 record 堆开销 |
| External - Write/merge 阶段 | **每个 (K,field) group 的 `dict map[string]PostingRef` 全量驻留** | ✅ 已治理 | **7.2：`streamingDict` 替代 map（已完成）** |
| External - Write 阶段 | AC field 整棵 trie + DAT 数组在内存编译 | ❌ | 7.4：按 group 释放 + 评估 streaming DAT |

**最高优先级单点：External 的 group dict（`segment_builder_external.go:359/421/438`）**。merge 输出本身已按 `(K,field,term)` 全序到达，dict 完全不需要 `map` 暂存——已通过 `streamingDict` 落地，见 7.2。

#### 7.1 交叉验证：峰值来源的代码级定位

**InMemory（`segment/segment_builder_mem.go`）**

- 主峰值：`fieldData map[int]map[string]*FieldData`，其中 `FieldData.Postings map[string][]core.EntryID`（:24-27）。`AddPosting`（:107-114）把**每一条 EntryID 全量追加进堆**，直到 `Write()` 才落盘。峰值 ≈ 全量 posting 字节（8B/EntryID）+ map/slice 头开销 + term 字符串。**设计上没有任何 spill 出口**——这与文档第一阶段"InMemory 仅适合测试/小 delta/受控 chunk"的定位一致，属于**已知且刻意**的取舍，不是 bug。
- 次峰值：`Write()` 内对每个 posting slice 原地 `sort.Slice`（:138-146），不额外分配；但 `WriteFlatPostingList`（`posting_list.go:116`）会为每个 term 再分配一份序列化 buffer（瞬时、可回收）。
- delta 链路：`builder/artifact.go:277` 的 `buildSegmentsToDir` 用 `new(bytes.Buffer)` 收集整段 segment 后再 `AtomicWriteFile`，相当于在 InMemory 峰值之上再叠一份整段字节。

**External（`segment/segment_builder_external.go`）**

- record 阶段**已受控**：`AddPosting`（:126-139）累积到 `maxRecs`（默认 `defaultMaxPostingsInMemory = 1_000_000`，:21）即 `flushRun()` 落盘 sorted run；`compactRuns`（:265）限制同时打开的 run reader ≤ `maxOpenRunReaders=64`，避免 fan-in 过大。这部分是教科书式 external sort，峰值由 `MaxPostingsInMemory` 显式封顶。
- **Write/merge 阶段仍有不可 spill 峰值**：
  1. **group dict**：`writeMergedBlocks`（:326）对每个 `(K,field)` group 维护 `dict = make(map[string]PostingRef)`（:421），merge 过程中**该 group 的全部 distinct term 都驻留 map**，直到 `finishGroup` 写完 dict/AC 才释放（:438、:395-399）。单 field term 百万级时，这个 map（term 字符串 + 16B PostingRef + map bucket 开销）就是峰值。**而这是可避免的**——见 7.2。
  2. **AC 编译**：AC field 在 `finishGroup`（:372-392）一次性 `NewStaticACBuilder` 建整棵 trie 再 `Compile`。`ac_builder.go` 的 `buildDAT`（:184）分配 `len(nodes)*2` 的 `base/check/used/revMap` 数组，`Compile`（:288）再分配 `outputData`。整棵自动机必须在内存成形，峰值与该 field 的 term 总量成正比。
- 单条 record 开销：`postingRecord{K int, Field string, Term string, Entry EntryID}`（:65-70）。注意 record 在内存里 `Field/Term` 都是 `string` header（16B 各），run 文件里 field 已压成 dense uint16（:623-644 注释），但**内存中的 `records` slice 仍持有完整 string header**，且 `openPostingRunReader.Next`（:661-682）每条都 `string(term)` 新分配。

**range / wildcard 旁路**

- range 间隔 `rangeData map[int]map[string][]Interval` 在两个 builder 都**常驻内存**（mem :40、external :50），但注释已说明其数量受 range predicate 数约束，远小于 EQ posting 流（external :141-144），可接受。
- wildcard 在 External 已通过 `builder/entry_spill.go` 的 `entryRunAccumulator` 做了 spill + k-way merge（`artifact.go:358/399/410`），是已落地的"构建期 spill"范例，可作为 7.2 的实现参照。

#### 7.2 建议一（最高 ROI）：External group dict 改为 streaming，去掉 `map` ✅ 已完成

**依据**：`writeMergedBlocks` 的主循环从堆里 pop 出来的 record **已经按 `(K,field,term)` 全序到达**——同一 term 的所有 entry 连续出现并被合并成一个 posting，写完即记录 dict 条目。也就是说 **term 是按字典序、一次性、不回头地产生的**，完全满足 `FlatDict` 的有序写入前提。原先却把它们攒进 `map[string]PostingRef` 等到 group 结束再 `WriteFlatDict`，白白把整个 group 的 term 表压在堆上。

**实际实现**：新增 `streamingDict` 类型（`segment/segment_builder_external.go:684-768`），替代 `map[string]PostingRef`：

```go
type streamingDict struct {
    items []streamingDictItem  // 定长 item 数组，24B/条
    terms []byte               // term 字节连续拼接，无独立 string 分配
}

type streamingDictItem struct {
    termStart    uint32 // term 在 terms 中的字节偏移
    termLen      uint32 // term 字节长度（AC field 重建 term 用）
    postingCount uint32
    postingOff   uint64
}
```

`Add(term, ref)` 在 merge 过程中逐条追加，`Bytes()` 在 group 结束时一次性序列化为 FlatDict 二进制。`Reset()` 用 `[:0]` 保留底层数组容量，跨 group 复用。

**与原始设计的差异**：原始设计建议两段临时文件做到 `O(1)` 峰值，实际实现选择了更简单的内存累积方案（`O(distinct terms)` 但无 map bucket 开销）。理由：
- 相比 `map[string]PostingRef`，已消除 map bucket 开销（~50B/条）和独立 string 分配，单 field 百万 term 场景从 ~102MB 降至 ~44MB（约 **57% 降幅**）
- 非 AC field 的 dict 写完即释放，不会跨 group 累积
- 避免临时文件 IO 复杂度；若未来 profiling 显示单 group dict 仍是瓶颈，可再升级为两段文件方案

**AC field 兼容**：`finishGroup` 中通过 `dict.terms[item.termStart : item.termStart+item.termLen]` 重建 term 字符串，顺序与 merge 到达顺序（字典序）一致，`acBuilder.Add` 的输入顺序与改前 `sort.Strings(terms)` 后一致。AC field 与非 AC field 统一走 `streamingDict`，无需分支。

**收益**：消除 External Write 阶段最大的不可控峰值；对超大单 field（百万 term）尤其明显。
**风险**：低。物理格式字节不变（reader 无需改），只改 builder 写入方式；两 builder 字节一致性测试（`TestExternalBuilderMatchesBuilderAcrossRuns` 等）直接复用回归。

#### 7.3 建议二：降低 External record 阶段单条堆开销

- `postingRecord` 内存态可考虑用 `fieldID uint16` 替代 `Field string`（run 文件已经这么做了，只是内存结构没跟上），把 `records` 的每条从"两个 string header"降到"一个 uint16 + 一个 term string header"。
- `postingRunReader.Next`（:661-682）每条 `string(term)` 分配可改为**复用 term buffer**（heap item 比较只需 `[]byte`，仅在确定要落地为 posting 时才转 string）——这正是原第 6 节"ExternalBuilder run merge 减少 term 分配"的内容，与本节同源，建议合并推进。
- 这些不改变峰值的"高度"（峰值仍由 `maxRecs` 封顶），但降低 GC 压力与等量内存下可容纳的 record 数，间接允许调大 `MaxPostingsInMemory` 以减少 run 数 / merge 轮次。

#### 7.4 建议三：AC field 构建期内存治理

- 现状：AC trie + DAT 必须整体在内存编译（`ac_builder.go`），无法 spill。短期**正确性无虞**，但单 field 海量 term 时是硬峰值。
- 低成本改进：确保 `finishGroup` 结束后 `acBuilder` 及其 `nodes/outputs/base/check` 立即可回收（当前是局部变量，作用域结束即可 GC，**已天然满足**，只需避免在更大作用域持有引用）。
- 进阶（高成本，低优先级）：streaming DAT 构建复杂度高（fail 指针需要 BFS 全图），不建议自研；若真触达瓶颈，优先考虑"按 K 分片建多个较小 AC 块"或外部成熟 DAT 库。

##### 7.4.1 调研备查：cedar 风格增量 double-array 能否流式构建 AC（结论：暂不实现）

针对"当前 AC 必须攒全量 term 再一次性 build，能否参考 cedar（`adamzy/cedar-go`、`vcaesar/cedar`）做增量/流式构建并提升线上查找性能"做了专项调研，结论是**暂不引入**，理由如下，备查：

1. **不存在常量内存的"真流式" AC 构建**。cedar 本质是 key→value 的**增量 double-array trie**（解决 goto trie 的逐条插入），它**不计算 Aho-Corasick 的 fail/output 链**。即便用 cedar 增量建 goto trie，fail 指针仍必须在所有 term 到齐后做一次**全图 BFS**（语义依赖完整 trie）。因此峰值仍是 `O(states)`，与现有 `buildFailPointers` 一致——这点与本节"fail 指针需要 BFS 全图"的判断吻合。

2. **查找性能无可承诺的提升**。现有 `ac`（`ac_mmap.go`）已是 mmap `base/check` 双数组，`transition` = `base[state]+sym` 一次访存 + `check[ns]==state` 一次访存，已接近此类结构下限。cedar 的 XOR 寻址（`base^label`）访存次数相同、XOR 与 ADD 同为单周期 ALU op，**逐指令等价，无加速**。任何"更快"只可能来自构建产生的状态编号/空洞分布不同带来的 cache 布局副作用，**不保证、需实测**，不能作为卖点。

3. **外部 cedar 库不可直接用**。`vcaesar/cedar` 虽含 `aho.go` 但**无 mmap 序列化**（堆上可变结构，违背本库 zero-copy mmap 根基）；cedar 原生 **byte(0-255) 字母表**会把 UTF-8 多字节字符（中文/emoji）拆成多个状态转移，**破坏 rune 级匹配语义**，与现有 ac + `anknown/ahocorasick` 差分 oracle 不一致。本库 AC 的 dense-rune 符号表（字母表压缩到实际出现字符数）正是支持 CJK/emoji 的根基，不能丢。

4. **真实收益只在构建期内存峰值（约 -30%~50%）**：省掉"链式 trie（`flatNode`）"与"DAT 双数组 + `used/revMap/stateMap` 辅助"的双份中间表示。但代价是引入全仓最复杂的数据结构（free-list 块管理 + resolve 冲突重定位），并需保证增量插入的**字节确定性**（两 builder bytes.Equal），还要承担双 AC 块类型并存的维护负担。**ROI 不划算**。

5. **若未来确需降 AC 构建峰值，优先低风险路线**：在现有 `StaticACBuilder.Compile` 内部优化——及早释放链 trie、复用/原地化 `buildDAT` 的 `used/revMap/stateMap` 辅助数组、按 K 分片建多个较小 AC 块——**保持 on-disk 格式与 reader 完全不变**，避免新增块类型的格式/reader/字节一致性三重风险。

> 备注：若将来真要做 cedarac，已论证的最优工程取向是：作为**并存新块类型**（不替换 ac）、**保留加法寻址**（复用现有 reader 语义、查找持平、风险最低）、reader 侧**抽取共享 `datMatcher`** 让 ac/cedarac 复用同一份 `transition/step/readOutputs` 以杜绝行为漂移。

#### 7.5 建议四（长期可选）：Dict 结构演进（原第 7 节内容）

当前 `FlatDict` 是 sorted term array + binary search，简单可靠。若单 field term 数达到百万级，可考虑：

- `FlatHashDict`：mmap-friendly hash table，查询 O(1)，实现复杂度适中；
- block term dictionary：分块 term index + prefix-compressed term block（顺带降低 term 字节区体积，对 7.2 的 streaming writer 友好）；
- FST/BlockTree：更接近 Lucene/Tantivy，但实现复杂度较高（详见第 8.1 节对 FST 的 ROI 分析）。

短期建议优先做 7.2（streaming group dict），不急于上 FST。

#### 7.6 落地优先级建议

1. ✅ **7.2 External streaming group dict**：已完成。`streamingDict` 替代 `map[string]PostingRef`，消除 group dict 的 map bucket 开销和独立 string 分配，单 field 百万 term 场景内存降幅约 57%。
2. **7.3 record/merge term 复用**：与原第 6 节合并，中等收益，降 GC 压力。
3. **InMemory 维持现状契约**：明确"大规模必须走 External"，必要时在 `BuildSegmentsFromDocs` / delta 链路按文档量阈值自动切换到 External，避免误用 InMemory 撞峰值。
4. **7.4 / 7.5** 触发条件高，长期排期。

## 8. FST 与 RoaringBitmap 选型深度分析

社区常把 "Lucene 用 FST 做词典、用 RoaringBitmap 做 posting" 当成标准答案。但能否套用到本库，必须回到本库两个**硬约束**：

- **约束 A（posting 元素不是 DocID，而是带语义位的 EntryID）**：`core.EntryID` 是 `uint64`，低位编码 include/exclude 标志，高位是 `ConjID`（含 K / ConjIndex / DocID）。检索算法 `retrieveK` 依赖 EntryID 的**全序**来做多路归并求交，并用最低位区分 include/exclude 实现短路排除。posting 不是"文档集合"，而是"带方向的条目序列"。
- **约束 B（cursor 接口契约）**：`core.PostingIterator` = `Current() EntryID` + `SkipTo(target EntryID) EntryID` + `Term()`。`FlatPostingList`/`SliceIterator` 都是有序数组上的二分 `SkipTo`。任何替代结构必须能高效实现 `SkipTo` 且返回的是 **EntryID**。

下面分别分析两种结构在这两个约束下的适配性。

### 8.1 FST 用在词典层（dict），不是 posting 层

首先厘清：FST 在 Lucene 里解决的是 **term → posting 指针** 的映射（词典压缩 + 前缀共享），它**不参与 posting 求交**。所以 FST 只能替代本库的 `FlatDict`，与 `retrieveK` 无关，不触碰约束 A/B。

- **能解决的问题**：超大词典（单 field 百万级 term，尤其前缀高度共享的字符串/中文）的内存与体积。FST 把公共前缀/后缀折叠成 DAG，比 `FlatDict` 的 "sorted term array + 全量 term bytes" 省内存，且天然支持前缀/范围枚举。
- **困难点（为什么不急着上）**：
  1. **构建复杂、需要全量 term 预排序**：FST 构建器要求 term 按字典序 push，本库 `ExternalBuilder` 的 group dict 当前是 `map[string]uint64` 最后排序——上 FST 要改成有序流式 build，和外排 run merge 的 term 顺序耦合，工程量大。
  2. **mmap 零拷贝难度高**：本库其它 block 都是定长数组 + `unsafe.Slice` 直接映射。FST 是变长字节图，遍历需要状态机解码，无法像 `[]uint32` 那样零成本映射；要么引入 `blevesearch/vellum`（之前 `go mod tidy` 刚移除），要么自实现，复杂度远高于当前 `FlatDict`。
  3. **收益门槛高**：广告定向字段基数通常可控（枚举值、分桶值），`FlatDict` 的 `O(log n)` 二分 + zero-copy 已足够。FST 的收益只有在"单 field term 百万级 + 前缀高度共享"才显著。
- **结论**：FST 是**词典层的可选演进**，与 posting/检索解耦，不存在"架构不适配"，只是**ROI 低 + mmap 工程成本高**，排在 posting ref / AC output 之后。真要做，建议直接复用 vellum 而非自研。

### 8.2 RoaringBitmap 用在 posting 层的根本困难

RoaringBitmap 看似能替代 posting list（压缩 + 快速集合运算），且 `roaring` 库确实提供了 `ManyIntIterator` / `AdvanceIfNeeded`（即 skip-to 语义），表面满足约束 B。但深入看，有三个**结构性**困难：

#### 困难 1：posting 元素是 64-bit EntryID，roaring 主力是 32-bit
- `roaring.Bitmap` 是 32 位域；64 位要用 `roaring64.Bitmap`。本库 `EntryID` 是**满 64 位**（ConjID 已用满高位，低位是 include 标志），无法降到 32 位。
- `roaring64` 的实现是"高 32 位分桶 → 每桶一个 32 位 roaring"，**它的 `Iterator` 不保证像 `[]uint64` 那样是单一紧凑有序流**，且 `roaring64` 的迭代器/skip 性能与内存特征明显弱于 32 位版本。对一个**本来就有序、且只需顺序 + skip-to** 的 posting，roaring64 是"杀鸡用牛刀"且更慢。

#### 困难 2：EntryID 的 include/exclude 语义被 bitmap "集合化" 抹掉
这是**最根本**的不适配：
- bitmap 的语义是"元素在/不在集合"。但本库 posting 里 include 条目和 exclude 条目是**两条不同方向的记录**，`retrieveK` 靠 `EntryID` 最低位 + 全序归并来在求交时**短路排除**（`engine/searcher.go` 的 exclude 分支）。
- 如果把 EntryID 直接塞进一个 bitmap，include/exclude 只是 EntryID 数值不同的两个 bit，bitmap 不理解它们的配对/短路关系。要恢复语义就得**拆成两个 bitmap（include set / exclude set）分别迭代再合并**，这反而把"一次有序归并"变成"多 bitmap 求并/求差"，破坏了 `retrieveK` 的 K-Groups 单次扫描模型。
- 换句话说：**posting 不是集合，是带方向的有序条目流**。bitmap 擅长集合代数（AND/OR/ANDNOT），而 `retrieveK` 要的是"多路有序流按 EntryID 对齐 + 在对齐点判断 include/exclude 计数"。两者计算模型不同。

#### 困难 3：稀疏 posting 下 bitmap 反而更占空间、skip 退化
- RoaringBitmap 的压缩优势在**稠密**位集（如"命中该 term 的 DocID 占全集比例高"）。但广告定向的倒排链很多是**稀疏**的（某个具体定向值只命中少量 conjunction）。
- 稀疏时 roaring 退化为 array container（本质就是 `[]uint16` + 分桶 overhead），相比本库 `FlatPostingList` 的紧凑 `[]uint64`，在 64 位场景**既不省空间也不省 skip 成本**，还多了容器层间接。
- 本库 `SkipTo` 是 zero-copy `[]EntryID` 上的二分，已是 O(log n) 且 cache 友好；roaring64 的 advance 要跨高位桶定位，常数更大。

#### 那 bitmap "能支持 cursor/skipto" 这点对不对？
对，但**对的是 DocID 集合层，不是 EntryID posting 层**。本库其实**已经在正确的位置用了 roaring**：
- `core.DocIDCollector` / `result_collector.go` 用 `roaring64` 做**结果 DocID 去重**；
- `engine/composite.go` 的 `BitmapDocSet`、`core/live_docs.go` 的 `LiveDocs` 用 bitmap 做 **changed/deleted/live 集合的 Contains 过滤**。

这些都是**真正的 DocID 集合语义**（在/不在、求差、去重），bitmap 完美适配。所以本库的现状是 **"posting 层用有序 EntryID 数组 + cursor 归并，集合层用 roaring"**——这恰好是与 Lucene 不同但**更贴合 VLDB09 K-Groups 算法**的正确分工。

### 8.3 选型结论矩阵

| 维度 | FlatPostingList（现状） | RoaringBitmap posting | FST dict | FlatDict（现状） |
|---|---|---|---|---|
| 适配 EntryID 64 位 + include/exclude 语义 | ✅ 原生 | ❌ 语义被抹平，需拆双 bitmap | n/a（不在 posting 层） | n/a |
| 适配 `retrieveK` 单次有序归并 | ✅ 全序天然支持 | ❌ 变成集合代数，破坏模型 | n/a | n/a |
| `SkipTo` 性能 | ✅ zero-copy 二分 | ⚠️ roaring64 advance 常数大 | n/a | n/a |
| 稀疏倒排链空间 | ✅ 紧凑 []uint64 | ❌ 容器 overhead | n/a | n/a |
| mmap zero-copy | ✅ unsafe.Slice | ⚠️ 需反序列化容器 | ❌ 变长图难映射 | ✅ |
| 超大词典内存/前缀压缩 | n/a | n/a | ✅ 强 | ⚠️ 全量 term bytes |
| 工程/构建复杂度 | 低 | 中高 | 高 | 低 |

**最终结论：**
1. **posting 层不要换 RoaringBitmap**。不是"bitmap 不支持 cursor"，而是 posting 的元素是带 include/exclude 方向的 64 位 EntryID、检索是有序归并而非集合代数；bitmap 的集合模型与之不匹配，且在 64 位/稀疏场景无空间优势。**保持 `FlatPostingList`**。
2. **bitmap 继续留在集合层**（结果去重、live/changed/deleted 过滤），现状已正确，无需改动。
3. **FST 仅作为 `FlatDict` 的长期可选演进**，触发条件是"单 field term 百万级 + 前缀高度共享"，且优先复用 vellum 而非自研；当前优先级低于 posting ref / AC output / 结构化 metadata。
4. posting 层若未来真有压缩需求（超大稠密链），更契合的方向是 **delta + varint / FOR（Frame-Of-Reference）块编码**（Lucene PForDelta 系），它**保留有序流与 cursor 语义**、可分块 skip，比 bitmap 更贴合本库模型——这是比 roaring 更值得评估的 posting 压缩路线。

## 总结

当前 segment 设计已经具备 immutable segment、footer metadata、zero-copy posting list、external-sort full build、真 mmap 加载 这些高性能索引系统的关键特征。

演进路线图（按依赖与 ROI 分批）：

- ✅ **已完成**：命名/职责拆分、加载校验策略、`MmapReader`→`SegmentReader` 改名、真 mmap 化加载、批次 A（AC 免 `[]rune` 分配 + blockKey 整数化）、**批次 B（segment v3：metadata 结构化 + dict PostingRef + AC output PostingRef）**、**批次 C（External streaming group dict：`streamingDict` 替代 `map[string]PostingRef`，消除 group dict 峰值）**。
- 🔧 **构建侧（可并行，低优先级）**：external builder run merge 降低 term 分配（第 6 节 / 7.3）。
- 🔭 **长期可选**：FlatDict → FST/block term dict（第 7.5 节，触发条件高）。

关于结构选型的定论（见第 8 节）：

- **posting 层保持 `FlatPostingList`**，不引入 RoaringBitmap。posting 元素是带 include/exclude 方向的 64 位 EntryID，检索是 `retrieveK` 的有序多路归并而非集合代数，bitmap 的集合模型不适配，且 64 位/稀疏场景无空间优势。
- **per-list roaring64 替换 `[]EntryID` 亦不可行**：roaring64 是堆上的容器树对象，无法像 `unsafe.Slice` 那样原地映射到 mmap 字节，要用必须在加载期或查询期反序列化到堆，直接破坏 zero-copy mmap + zero-alloc 根基（cursor 语义本身 roaring 能满足，但内存模型冲突）。
- **RoaringBitmap 保持在集合层**（结果去重、live/changed/deleted 过滤），现状已是正确分工。
- **FST 仅作为 `FlatDict` 的长期可选演进**，触发条件是单 field term 百万级 + 前缀高度共享，优先复用 vellum，优先级低于批次 B。
- posting 若未来需压缩，优先评估 **delta+varint / FOR 块编码**（保留有序流与 cursor/skip 语义），而非 bitmap。
