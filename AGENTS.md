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
| **Predicate** | `Predicate` | 单个逻辑条件，由 **Field** 和 **Constraint** 组成。例如 `age > 18`。 |
| **Constraint** | `Constraint` | 描述 Predicate 中的具体约束逻辑，包含 **Value**、**Operator** 和 **Incl/Excl**。 |
| **Assignment** | `Assignments` | 查询时输入的属性值集合，例如 `{age: 20, city: "bj"}`。 |
| **Field** | `FieldName` | 规则、Schema 和查询中的字段名；是 `string` 的别名。 |
| **K (Include count)** | `Conjunction.IncludeCount()` / `ConjID.IncludeCount()` | 合法 Conjunction 中的 **Include** 约束数量（每字段最多一个）。编码在 ConjID bits 52-59、EntryID bits 56-63，使排序后的 EntryID 天然按 K 分组，无需物理分桶；`FieldCount()` 另统计全部字段数。 |
| **Z-Entry (Wildcard)** | `WildcardFieldName = "_Z_"` | K=0 的 Conjunction 产生的 EntryID。无 Include 约束，始终产生候选，但仍可被 Exclude 否决。索引引擎和段存储中统称为 wildcard。 |
| **ConjID** | `ConjID` | 描述一个 Conjunction 身份的 64 位整数，编码了 K、DocID、子句 Index 和负号位。 |
| **EntryID** | `EntryID` | Conjunction 与 Include/Exclude 极性的排序键，同一个键可出现在不同 Posting List 中，不包含字段或 term 身份。K 编码在 bits 56-63。 |
| **Posting List** | `PostingIterator` | `core` 包中的倒排迭代器接口，`SliceIterator`（内存）和 `flatPostingCursor`（mmap）均实现此接口。`FieldCursor` 封装若干个 PostingIterator 做多路归并。 |

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
- **`segment`**: 包含 `SegmentBuilder`（统一构建器：按 `SegmentBuilderOptions` 选择全内存或溢写外部排序，两种配置输出字节一致）和 `SegmentReader`（校验后读取；对齐的 little-endian posting/AC 数组可零拷贝）。通过 `FlatDict` 和 `FlatPostingList` 实现高密集度存储。通过 `IndexReader`/`IndexBuilder` 接口 + `RegisterIndex` 注册表支持可插拔索引容器（内置 `ac_matcher`、`ext_range`）。
- **`engine`**: 包含 `BooleanEngine` 和底层的 `matchConjunctions` 算法，在不触碰任何物理布局的情况下基于 `FieldCursor` 完成求交运算。通过 `SchemaCodec` 将 Assignment 编码为 `EncodedQuery{Value}`，再由预解析的 `ResolvedField.MatchQuery` 按 `IndexType` 调用容器，支持自定义容器类型。
- **`core`**: 处于依赖最底层，定义基础类型、ID 位元结构、游标算法、collector 和评分原语。`builder` 与 `engine` 均依赖 `parser` 和 `segment`，两者互不依赖；`parser` 不依赖 `segment`。

### 3. Error Handling
- **Recoverable Errors**: 返回 `error` 类型。
- **Configuration Errors**: 构建阶段配置或解析错误返回 `error`，不 panic。
- **Runtime Safety**: 在检索热路径（Retrieve）中严禁 Panic，因为该路径直接在用户的线上应用里被高并发调用。

### 4. Performance Considerations
- **Zero-Copy / Low-Allocation**: `SkipTo` 等核心循环保持零分配；完整 Retrieve 的编码、游标组合和独立结果允许受控分配，以 benchmark 跟踪。复用 `ResultCollector`。对齐的 little-endian 存储可直接映射；其他端序或非对齐输入显式解码，保证正确性。
- **Collector Contract**: 普通 collector 每次查询只接收去重后的可见 DocID；显式实现 `DuplicateTolerantCollector` 可自行去重。`CollectStop` 终止整个 full/delta 查询。collector 由调用方独占并在复用前 Reset。
- **Temporary Directory Ownership**: `SegmentBuilderOptions.TmpDir` 是调用方拥有的父目录。builder 只清理自己的私有子目录；构造成功后应 `defer Close()`，`Write()` 无论成功失败都会关闭并清理。
- **Production Snapshot**: 业务推荐一个 full 和一个累计 delta，各自可包含多个 segment；每轮 delta 覆盖当前 full 之后的全部变更，发布时替换旧 delta。底层保留多个连续 delta 的支持。
- **Index Maintenance Assessment**: 根包 `api_maintenance.go` 只提供是否合并 delta、重建并发布 full 的业务评估参考，不执行构建、发布或调度。`TimeSinceFullBuild` 明确定义为距当前 full 构建完成经过的时间，由业务提供。
- **Limits**:
    - `DocID` 范围: `[-(2^43-1), 2^43-1]`
    - 单个 Document 的 Conjunction 数量限制: < 256
    - 最大 Include 数量 (K): < 256

## Documentation Contract (文档契约)

**单一文档根目录：** 所有规范性文档只放在 `docs/`。禁止再新增 `static/docs` 或其它并行文档目录。`static/design/` 仅用于论文、图片、drawio 和背景笔记等**非规范性参考资料**。

### 目录与职责

| 目录 | 职责 | 当前性要求 |
| :--- | :--- | :--- |
| `docs/guide/` | 使用方指南：快速开始、最佳实践、示例、API 参考 | 必须与当前 API/行为一致 |
| `docs/architecture/` | 当前实现：包边界、构建/查询路径、存储格式、生命周期 | 必须与代码一致 |
| `docs/architecture/indexes/` | 每个索引容器一篇文档 | 必须与 `IndexType` 实现一致 |
| `docs/design/` | 当前有效的独立设计说明 | 必须反映已落地或已批准的设计 |
| `docs/history/` | 已归档的设计、评审、规格与实施计划 | 不代表当前实现，仅用于追溯 |
| `static/design/` | 论文、设计图、背景笔记等参考资料 | 非规范性，不得作为 API 依据 |

- `docs/README.md` 是文档总索引。新增、删除、重命名文档时必须同步更新。
- 每个类别应有自己的索引：`docs/architecture/indexes/README.md`、`docs/history/README.md` 等。
- 历史文档首行必须保留 banner：
  `> 历史设计/实施记录，保留当时方案；当前 API 与行为以代码、README 和 docs/README.md 为准。`
- 文档中的路径引用（包括 Go 代码注释）必须指向当前位置，例如 `docs/history/geo/geo-design-v3.md`。

### 命名约定

- 普通文档使用 lowercase kebab-case：`best-practices.md`、`quick-start.md`。
- 目录索引固定为 `README.md`。
- 历史计划/规格使用 `YYYY-MM-DD-<slug>.md` 日期前缀；早期设计记录保留原文件名（如 `geo-design-v2.md`）。
- 容器文档使用注册的 `IndexType` 名（snake_case），如 `mph_dict.md`、`ac_matcher.md`，保证文件名与 `IndexType` 一一对应。
- 一个文件只讲一个主题；避免 `notes.md`、`misc.md` 这类无法归类的名称。

### 内容约定

- `docs/guide/`、`docs/architecture/`、`docs/design/` 只描述**当前实现**，不写“旧版/以前”的方案；历史内容放 `docs/history/`。
- 每篇文档以唯一 H1 开头，并至少被 `docs/README.md` 或所属类别索引链接一次。
- 类型名、字段名、函数名、配置项必须与代码一致；示例代码应可编译，伪代码必须明确标注。
- 不复制大段源码，优先链接到源文件（如 `segment/flat_dict.go`）。
- 二进制资产（PDF、PNG、drawio）放 `static/design/`，文档中使用相对链接引用。

### 链接约定

- 一律使用相对链接；禁止 `file://`、绝对路径、以及指向已删除目录（如 `static/docs/`）的链接。
- 从 `docs/guide/`、`docs/architecture/`、`docs/design/`、`docs/history/` 引用仓库根使用 `../../`；从更深一层（如 `docs/architecture/indexes/`、`docs/history/geo/`）使用 `../../../`。
- 移动/重命名文档必须使用 `git mv`，并同步更新：根 `README.md`、`docs/README.md`、类别索引、跨文档链接、代码注释中的路径。
- 提交前检查 `README.md` 与 `docs/` 下所有 Markdown 相对链接可解析。

### 变更流程

1. 代码/API 变更时，在同一个提交中更新对应的当前文档。
2. `docs/history/` 中的结论不再改写；仅在路径或链接失效时修正路径与 banner。
3. 新设计先放 `docs/design/`；方案落地或被取代后，移入 `docs/history/` 并加 banner。
4. 文档结构调整后至少运行 `go build ./...`，并完成相对链接检查。

## Code Structure
```text
/
├── api_*.go                # 按 document/query/score/build/segment/snapshot/maintenance 分组的公共 API
├── docs/                   # 规范性文档（契约见 Documentation Contract）
│   ├── README.md           # 文档总索引
│   ├── guide/              # 使用指南
│   ├── architecture/       # 当前架构与索引容器文档
│   ├── design/             # 当前独立设计
│   └── history/            # 归档设计与实施计划
├── builder/                # 离线构建，汇聚成 Segment
│   ├── segment_build.go    # BuildSegmentFromDocs 逻辑
│   ├── index_builder.go    # FullIndexBuilder, DeltaIndexBuilder
│   └── delta.go            # DeltaPlan, Mutation types
├── engine/                 # 在线查询，执行布尔表达式匹配
│   ├── boolean_engine.go   # BooleanEngine 与 matchConjunctions 算法
│   └── composite_engine.go # CompositeEngine (full+delta merge)
├── container/              # 包外容器与编码器
│   ├── geo/                # proximitygeo: proximityhash 覆盖 cell → 纯 term 索引（无自定义容器）
│   └── example/            # 自定义 container/encoder 扩展模板（含端到端测试）
├── segment/                # Mmap 存储结构管理
│   ├── segment_builder.go  # SegmentBuilder (全内存 / 溢写外部排序)
│   ├── segment_reader.go   # SegmentReader (mmap/heap backing)
│   ├── field_index.go      # IndexReader/IndexBuilder 接口 + RegisterIndex 注册表
│   ├── flat_dict.go        # FlatDict
│   └── posting_list.go     # FlatPostingList
├── core/                   # 基础接口、结构与 ID 编码
│   ├── id_types.go         # ConjID / EntryID 编解码
│   ├── document.go         # Document, Conjunction
│   ├── predicate.go        # Predicate, Constraint, ValueOp, FieldName, Assignments
│   ├── cursor.go           # FieldCursor, SliceIterator
│   └── result_collector.go # DocIDCollector (roaring64)
├── loader/                 # 冷启动加载
│   ├── loader.go           # OpenIndex, LoadSnapshot
│   └── holder.go           # Holder (atomic live-reload)
├── manifest/               # 快照元数据
│   ├── manifest.go         # Manifest struct
│   └── publish.go          # PublishManifest (atomic CURRENT)
└── parser/                 # 值分词器与 Schema 编解码
    ├── encoder.go          # SchemaCodec, EncodedRecord, EncodedQuery
    ├── number_tokenizer.go # NumberTokenizer
    └── tokenizer.go        # ValueTokenizer interface
```
