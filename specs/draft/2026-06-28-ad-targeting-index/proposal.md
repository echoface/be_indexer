# 广告定向索引库 v2 需求提案

## 背景

目标是建设一个面向广告定向 serving 的布尔表达式索引底层库。业务侧从 DB 读取广告定向数据，完成必要的清洗、标准化与 DNF 化后，调用本库构建索引产物；线上 serving 加载索引后，对用户请求特征执行高性能检索，返回满足定向条件的广告或规则 ID。

现有仓库已经具备 VLDB09《Indexing Boolean Expressions》的核心雏形：`Document -> Conjunction -> Predicate` DNF 模型、K-Groups、`ConjID/EntryID` 64-bit 编码、posting cursor merge、exclude short-circuit 与 mmap-friendly segment。关键位置包括：

- DNF 模型：`core/document.go:13`
- Predicate 模型：`core/boolean_expr.go:20`
- K 计算：`core/document.go:194`
- 64-bit ID 编码：`core/id_types.go:14`
- 构建写 posting：`builder/doc_exporter.go:108`
- 检索 `retrieveK`：`engine/searcher.go:88`

## 目标

1. 保留 VLDB09 核心算法：DNF、K-Groups、posting list 多路归并、exclude short-circuit。
2. 继续使用 64-bit `ConjID/EntryID` 编码压缩，不将 SegmentID 编入 PostingID。
3. 固化 exclude 语义为 `exclude-on-present-value`：query 字段存在且命中排除值时才排除；query 缺字段不触发 exclude。
4. 引入 `manifest + 多 segment` 的索引快照模型，支持原子发布、回滚、checksum 与 schema hash。
5. 支持 full + delta 增量折中：长窗口全量索引 + 近期变更 delta 索引，查询时组合结果。
6. 建立测试与验证门槛：边界测试、exclude missing、full+delta update/delete/recreate、manifest 一致性、benchmark 与 shadow。

## 非目标

1. 不直接连接业务 DB 或消息队列。
2. 不负责广告排序、竞价、预算、频控。
3. 不在本阶段完整重做 Segment v2 二进制格式；先以 manifest/sidecar 管理快照一致性。
4. 不把 full 和 delta 强塞进同一个 `BooleanEngine` 并共享全局 `LiveDocs`。

## 关键设计决策

### 64-bit 编码

继续使用当前编码：

```text
ConjID: [reserved(4bit)|K(8bit)|ConjIndex(8bit)|Sign(1bit)|DocID(43bit)]
EntryID: [ConjID(60bit)|unused(3bit)|include/exclude(1bit)]
```

必须暴露给业务的硬限制：

- `DocID` 绝对值 `<= 2^43 - 1`；广告业务建议只用正 ID。
- 单个 Document 的 Conjunction 数 `< 256`。
- 单个 Conjunction 的 K `< 256`。
- 单条 posting list entry 数 `<= uint32 max`。

### exclude missing

语义固定为：

```text
rule: A in 1 AND B not in 2
query {A:1}      => match
query {A:1,B:3}  => match
query {A:1,B:2}  => not match
```

该语义与当前查询只为 query 中存在字段创建 cursor 的实现一致，相关代码在 `engine/searcher.go:187` 与 `engine/searcher.go:217`。

### full + delta

查询结果定义为：

```text
Result = (FullResult - ChangedDocsUnion) ∪ (DeltaResult - DeletedDocsFinal)
```

其中：

- `ChangedDocsUnion`：delta 窗口内发生 create/update/delete/recreate 的所有 DocID，用于剔除 full 中旧版本。
- `DeletedDocsFinal`：当前快照最终状态为 deleted 的 DocID。
- delta 构建前必须按 DocID 聚合，只保留最大 Version 的 mutation，避免 delete 后 recreate 被旧 tombstone 误杀。

## 交付范围

1. 方案文档：`proposal.md`、`design.md`、`tasks.md`。
2. `CompositeEngine / IndexSnapshot` 查询合并层。
3. manifest 结构定义与基础校验。
4. 64-bit 编码构建期校验与 posting list count 防溢出。
5. 单元测试覆盖：边界、exclude missing、full+delta update/delete/recreate。

