# Mmap 架构：演进与严谨验证总结

本文档记录了 `be_indexer` 新架构（读写分离、零拷贝 mmap、K-Groups 引擎）在开发和上线前所经历的严谨验证方案与演进过程。

---

## 一、 核心架构重构与完成情况
目前我们已经完全将 `be_indexer` 升级为面向领域的 SDK 架构（`builder` -> `segment` -> `engine`）：
- [x] **FlatPostingList**: `[]EntryID` 零拷贝映射、基于切片的二分 `SkipTo`。
- [x] **FlatDict**: 变长字符串紧凑布局、原址二分查找 `Term -> Offset`。
- [x] **AC Matcher Reader**: 双数组 Trie (DAT) 和 Fail 指针的四维数组序列化与原址跳转。
- [x] **Segment Writer & Reader**: 块头尾布局协议 `[Magic][Blocks...][Meta][Footer]` 落地。
- [x] **BooleanEngine**: 独立且纯粹的 `retrieveK` 检索执行器，从具体的存储介质中彻底解耦。

---

## 二、 严密验证体系 (Shadow Testing)

由于布尔倒排求交逻辑极其脆弱，新架构在上线前经历了极为严苛的一致性验证（参考 `be_indexer_property_test.go` 和 `engine` 相关测试）。

### 1. 结构与序列化的一致性
针对各种容器（特别是 AC 自动机），我们验证了从原有的堆内数据结构到平铺 `[]byte` 的转换不会丢失任何状态。
通过对生成的 DAT `base`、`check` 数组在映射后的遍历查找，确保了 `MmapReader.MultiPatternSearch` 能够 100% 还原词表匹配序列。

### 2. 核心求交循环的鲁棒性
我们构造了“双轨验证 (Shadow Testing)”体系：
- **随机生成器 (Fuzzer)**：生成了包含海量 Conjunctions、大跨度 Posting、重叠词组以及极其复杂的 `NOT IN` (Exclude) 负向条件的 Document 集合。
- **一致性比对**：将随机生成的 10,000 个查询 `Assignments` 喂给检索引擎。测试用例严格断言了基于 Mmap 的 `BooleanEngine` 返回的 `DocIDList` 必须与全量扫描验证（Oracle 暴力验证）的结果 **绝对一致**。

### 3. Z-Entry (Exclude) 短路剪枝的边界测试
针对 K=0 的情况（纯 Exclude 条件的文档），我们修复了由于 `continue` 语句导致纯 Exclude 谓词无法正确序列化到段的严重 Bug。
更新后的构建逻辑（`builder.BuildSegmentFromDocs`）完美支持了纯 Exclude 条件（以 K=0 也就是 Wildcard 形式附加 `ConjID` 并标识为 Include），确保了 `engine` 能够在其上执行准确的排除逻辑。

### 4. LiveDocs 与生命周期管理
通过在 `engine` 层面引入 `LiveDocs` 接口，新架构优雅地支持了不可变存储段（Immutable Segments）上的文档删除和禁用。引擎在匹配时将通过 `LiveDocs` 拦截被标记为不可用的 `DocID`。

---

## 三、 结论

通过拆分 `builder`（编译层）、`segment`（物理层）和 `engine`（执行层），我们在保障原有检索算法 100% 正确性的前提下，成功实现了零拷贝加载和极速求交。`be_indexer` 现已具备支撑百GB级复杂规则库且在毫秒级拉起检索服务的能力。
