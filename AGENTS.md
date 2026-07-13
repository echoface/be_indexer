# Agent Guidelines for be_indexer

## Project Overview
`be_indexer` 是一个高性能的布尔表达式索引库（Boolean Expression Indexing），实现了 VLDB 09 论文 《Indexing Boolean Expressions》 中的算法，并针对工程实践进行了读写分离、K-Groups 算法和零拷贝 (mmap) 重构。

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
| **K (Size)** | `Size` / `K` | 一个 Conjunction 中包含的 **Include** 约束数量。编码在 ConjID/EntryID 的高位 (bits 56-63)，使排序后的 EntryID 天然按 K 分组，无需物理分桶。 |
| **Z-Entry (Wildcard)** | `WildcardFieldName = "_Z_"` | K=0 的 Conjunction 产生的 EntryID。无 Include 约束，永远命中。索引引擎和段存储中统称为 wildcard。 |
| **ConjID** | `ConjID` | 描述一个 Conjunction 身份的 64 位整数，编码了 K、DocID、子句 Index 和负号位。 |
| **EntryID** | `EntryID` | 唯一标识一个倒排条目，基于 ConjID 增加了 Include/Exclude (1 bit) 信息。K 编码在 bits 56-63。 |
| **Posting List** | `TermIterator` / `PostingIterator` | `core` 包中的倒排迭代器接口，`SliceIterator`（内存）和 `flatPostingCursor`（mmap）均实现此接口。`FieldCursor` 封装若干个 PostingIterator 做多路归并。 |

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
- 对于核心检索算法的重构，必须通过 `BooleanEngine` 与原始数据的 Shadow Testing（对比随机查询结果）保证 100% 一致。
- 保证 `mmap` 的 Segment 文件生成与读取不受跨平台端序干扰。

## Development Guidelines

### 1. Code Style & Naming
- **Strict Typing**: 严格区分 `DocID` (int64), `EntryID` (uint64), `ConjID` (uint64)。
- **Naming Conventions**:
    - 统一使用 `builder` (离线构建), `segment` (物理段), `engine` (执行器), `core` (基础原语)。
- **Comments**: 复杂逻辑（尤其是位运算 `ConjID`/`EntryID` 的编解码、mmap 指针操作、Z-Entry 短路判断）必须包含清晰的注释（支持中文）。

### 2. Architecture & Design
该项目采用了强类型的读写分离架构，依赖关系单向向下，严禁反向依赖：
- **`builder`**: 包含 `BuildSegmentFromDocs`，负责将 `Document` 打平解析并推入 Segment 构建器。
- **`segment`**: 包含 `Builder` (用于二进制对齐序列化) 和 `SegmentReader` (零拷贝反序列化)。通过 `FlatDict` 和 `FlatPostingList` 实现高密集度存储。通过 `ContainerReader`/`ContainerBuilder` 接口 + `RegisterContainer` 注册表支持可插拔索引容器（内置 `ac_matcher`、`ext_range`）。
- **`engine`**: 包含 `BooleanEngine` 和底层的 `mergeCursors` 算法，在不触碰任何物理布局的情况下基于 `FieldCursor` 完成求交运算。通过 `EncodedQuery.Kind` default 分支路由到 `SegmentReader.ContainerQuery`，支持自定义容器类型。
- **`core`**: 处于依赖最底层，定义基础类型、常数与 ID 位元结构。

### 3. Error Handling
- **Recoverable Errors**: 返回 `error` 类型。
- **Configuration Errors**: 构建阶段配置或解析错误直接抛出错误。
- **Runtime Safety**: 在检索热路径（Retrieve）中严禁 Panic，因为该路径直接在用户的线上应用里被高并发调用。

### 4. Performance Considerations
- **Zero-Copy / Zero-Allocation**: 在 `engine.Retrieve` 路径中禁止堆内存分配。复用 `ResultCollector`。`segment` 读取必须直接基于 `[]byte` 切片操作。
- **Limits**:
    - `DocID` 范围: `[-2^43, 2^43]`
    - 单个 Document 的 Conjunction 数量限制: < 256
    - 最大 K Size (Size): < 256

## Code Structure
```text
/
├── builder/                # 离线构建，汇聚成 Segment
│   ├── doc_exporter.go     # BuildSegmentFromDocs 逻辑
│   ├── artifact.go         # FullIndexBuilder, DeltaIndexBuilder
│   └── delta.go            # DeltaPlan, Mutation types
├── engine/                 # 在线查询，执行布尔表达式匹配
│   ├── searcher.go         # BooleanEngine 与 mergeCursors 算法
│   └── composite.go        # CompositeEngine (full+delta merge)
├── segment/                # Mmap 存储结构管理
│   ├── segment_builder_mem.go    # InMemorySegmentBuilder
│   ├── segment_builder_external.go # ExternalBuilder (external sort)
│   ├── segment_reader.go   # SegmentReader (mmap/heap backing)
│   ├── container.go        # ContainerReader/Builder 接口 + 注册表
│   ├── flatmap.go          # FlatDict
│   └── posting_list.go     # FlatPostingList
├── core/                   # 基础接口、结构与 ID 编码
│   ├── id_types.go         # ConjID / EntryID 编解码
│   ├── document.go         # DNF, Conjunction, Predicate
│   ├── cursor.go           # FieldCursor, SliceIterator
│   └── result_collector.go # DocIDCollector (roaring64)
├── loader/                 # 冷启动加载
│   ├── loader.go           # OpenIndex, LoadSnapshot
│   └── holder.go           # Holder (atomic live-reload)
├── manifest/               # 快照元数据
│   ├── manifest.go         # Manifest struct
│   └── publish.go          # PublishManifest (atomic CURRENT)
└── parser/                 # 值分词器与 Schema 编解码
    ├── encoder.go          # SchemaCodec, EncodedQuery
    └── tokenizer.go        # ValueTokenizer interface
```