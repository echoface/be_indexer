# Agent Guidelines for be_indexer

## Project Overview
`be_indexer` 是一个高性能的布尔表达式索引库（Boolean Expression Indexing），实现了 VLDB 09 论文 《Indexing Boolean Expressions》 中的算法，并针对工程实践进行了优化（如 K-Index 分层、RoaringBitmap 压缩、增量构建等）。

目标场景：广告定向（Targeting）、推荐系统规则过滤、复杂规则引擎。

## Glossary (术语表)
**开发过程中必须严格遵守以下术语定义，避免混淆：**

| 术语 (Term) | 代码对应 (Go Type) | 说明 |
| :--- | :--- | :--- |
| **Document** | `Document` | 用户提交的一个完整布尔规则（DNF），包含多个 Conjunction（逻辑 OR 关系）。 |
| **Conjunction** | `Conjunction` | DNF 中的一个子句，由多个 Predicate 组成（逻辑 AND 关系）。 |
| **Predicate** | `Predicate` | 单个逻辑条件，由 **Field** 和 **Constraint (ValueExpr)** 组成。例如 `age > 18`。 |
| **Constraint** | `ValueExpr` | 描述 Predicate 中的具体约束逻辑，包含 **Value**、**Operator** 和 **Incl/Excl**。 |
| **Assignment** | `Assignments` | 查询时输入的属性值集合，例如 `{age: 20, city: "bj"}`。 |
| **K (Size)** | `Size` / `K` | 一个 Conjunction 中包含的 Predicate 数量，用于索引分层（K-Index）。 |
| **K-Index** | `KGroupsBEIndex` | 按照 K 值分组存储的倒排索引结构，是核心检索数据结构。 |
| **Entry ID** | `EntryID` | 唯一标识一个 Conjunction 的 ID。包含 `DocID`、`Size`、`Index` 等信息。 |
| **Posting List** | `EntriesHolder` | 倒排链容器接口，存储满足特定 Predicate 的所有 EntryID。 |

## Build & Test

```bash
# Run all tests (Recommended)
make test

# Build the module
make build

# Run formatting/vet
go vet ./...
```

**Testing Guidelines:**
- 使用 **GoConvey** (`github.com/smartystreets/goconvey/convey`) 进行 BDD 风格测试。
- 对于核心逻辑（如索引检索），推荐编写 **Property-based Tests**（参考 `be_indexer_property_test.go`），通过 Oracle（暴力解法）验证正确性。
- 保持高测试覆盖率，特别是对 `entries_holder` 和 `roaringidx` 包。

## Development Guidelines

### 1. Code Style & Naming
- **Strict Typing**: 严格区分 `DocID` (int64), `EntryID` (uint64), `ConjID` (uint64)。
- **Naming Conventions**:
    - 使用 `Predicate` 而不是 `BooleanExpr`。
    - 使用 `ValueExpr` 而不是 `BoolValues`。
    - 接口名以 `er` 结尾（如 `EntriesHolder`），实现类名清晰描述其特性（如 `CompressedKVHolder`）。
- **Comments**: 复杂逻辑（尤其是位运算、压缩算法）必须包含清晰的注释（支持中文）。

### 2. Architecture & Design
- **IndexerBuilder**: 负责文档的解析、验证和索引构建流程。支持全量构建和基于缓存的增量构建。
- **EntriesHolder**: 核心抽象，不同的字段类型（String, Int, AC Match）可以有不同的 Holder 实现。
    - `DefaultEntriesHolder` (CompressedKV): 适用于高基数、等值查找。
    - `ACEntriesHolder`: 适用于多模式串匹配（Aho-Corasick）。
    - `OptimizedRangeHolder`: 适用于数值范围查询（线段树/区间树优化）。
- **RoaringBitmap**: 在底层存储中使用 RoaringBitmap (`roaringidx` 包) 优化空间和集合运算性能。

### 3. Error Handling
- **Recoverable Errors**: 返回 `error` 类型。
- **Configuration Errors**: 启动阶段配置错误建议使用 `util.PanicIf` 快速失败。
- **Runtime Safety**: 在检索热路径（Retrieve）中严禁 Panic，必须捕获并返回错误。

### 4. Performance Considerations
- **Allocations**: 在 `Retrieve` 路径中尽量减少内存分配。复用 `Collector` (使用 `sync.Pool`)。
- **Concurrency**: 索引构建通常是单线程的（为了保证 ID 分配顺序），但检索是并发安全的（Read-Only）。
- **Limits**:
    - `be_indexer` DocID 范围: `[-2^43, 2^43]`
    - `roaringidx` DocID 范围: `[-2^56, 2^56]`
    - 单个 Document 的 Conjunction 数量限制: < 256

## Code Structure
```text
/
├── be_indexer.go           # 核心接口定义
├── document.go             # Document/Conjunction/Predicate 定义
├── entries_holder.go       # Posting List 容器接口
├── index_builder.go        # 索引构建器
├── be_indexer_kgroups.go   # K-Index 核心实现
├── roaringidx/             # 基于 RoaringBitmap 的独立实现
├── holder/                 # 各种特定类型的 Holder 实现 (AC, Range)
├── example/                # 示例代码 (务必保持更新)
└── static/                 # 设计文档和论文
```
