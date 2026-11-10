# be_indexer API 参考

业务接入使用根包 `github.com/echoface/be_indexer`；容器扩展直接使用 `parser`、`segment` 和 `core`。根包通过公开类型与普通函数提供入口，按职责组织在 `api_*.go`，不提供可替换的全局函数变量、全局 logger 或 collector 池接口。

## API 分组

| 文件 | 主要 API |
|---|---|
| `api_document.go` | `Document`、`Conjunction`、`Predicate`、`Constraint`、`Schema`、`Assignments`、值构造函数 |
| `api_query.go` | `NewEngine`、`NewDocIDCollector`、`ResultCollector`、`QueryOption`、查询 observer |
| `api_score.go` | `NewTopKCollector`、`ScoreSource`、`NewBlockMaxScoreTable`、评分表 |
| `api_build.go` | `BuildSegment`、`BuildSegments`、`NewFullIndexBuilder`、`NewDeltaIndexBuilder`、Mutation |
| `api_segment.go` | `NewSegmentReader`、`NewSegmentReaderWithOptions`、`OpenSegmentFile`、校验选项 |
| `api_snapshot.go` | `OpenIndex`、`NewIndexHolder`、`LoadSnapshot`、Manifest 构造与发布 |
| `api_maintenance.go` | `IndexMaintenanceStats`、`IndexMaintenancePolicy`、`DefaultIndexMaintenancePolicy`、`CollectIndexMaintenanceStats`、`EvaluateIndexMaintenance`、`EvaluateIndexMaintenanceStats`；仅提供业务评估参考 |

可执行示例见根目录 `example_test.go`，运行 `go test . -run Example`。完整声明以 `go doc github.com/echoface/be_indexer` 为准。

## 规则模型

- `Document` 是 DNF 规则；`Cons []*Conjunction` 之间为 OR，`ID` 是业务主键，`Version` 是业务版本。
- `Conjunction.Constraints` 为 `map[FieldName][]*Constraint`。字段间为 AND；同字段最多一个 Include，可有多个 Exclude。
- `NewConjunction().In(field, values)` / `.Include(field, values)` 添加等值 Include；`NotIn` / `Exclude` 添加 Exclude。`Between` 表示闭区间；`GreaterThan`、`LessThan` 是严格比较。
- `K` 是 Include Constraint 数。K=0 产生 wildcard 候选，但仍受 Exclude 和可见性过滤约束。
- `DocID` 范围为 `[-(2^43-1), 2^43-1]`；Conjunction 数和 K 均小于 256。全量 corpus 内 DocID 必须唯一。
- `Assignments` 是 `map[FieldName]Values`，其中 `Values` 为 `any`，值的形态由 Encoder 决定。

`Predicate` 由 `Field FieldName` 与匿名嵌入的 `Constraint` 组成。`Constraint` 包含 `Value`、`Operator ValueOp` 和 `Incl`。`ValueOpEQ` 表示等值或集合成员关系，`ValueOpGT/LT` 为严格比较，`ValueOpBetween` 为闭区间；具体可用运算符由字段编码器决定。`FieldName` 是 `string` 的别名。

`FieldCount()` 返回 `Constraints` 的字段键数量；`IncludeCount()` 返回其中含 Include 约束的字段数，合法规则中等于 K。例如 `city in ["bj", "sh"] AND city not in ["gz"] AND age > 18 AND device not in ["bot"]` 有 3 个字段、4 个约束，`FieldCount()` 为 3，`IncludeCount()` 为 2。值列表长度不增加 K。底层 `ConjID.IncludeCount()` 从 ID 位编码中读取相同的 K。

显式构造约束并绑定字段：

```go
constraint := be_indexer.NewConstraint(be_indexer.ValueOpGT, int64(18), true)
predicate := be_indexer.NewPredicateWithConstraint("age", constraint)
conjunction := be_indexer.NewConjunction().AddPredicates(predicate)
```

规则序列化使用 `cons`、`predicates`、`inc`、`value`、`operator` 等 JSON 字段；`Conjunction.Constraints` 的 JSON 名为 `predicates`。

```go
fields := be_indexer.Schema{
    "city": {IndexType: be_indexer.IndexNameDefault},
    "age":  {IndexType: be_indexer.IndexNameExtendRange, Encoder: "ext_range"},
}
doc := be_indexer.NewDocument(1).AddConjunction(
    be_indexer.NewConjunction().Between("age", 18, 25).Include("city", "beijing"),
)
```

`FieldOption.IndexType` 选择物理容器，`Encoder` 选择值编码器。默认等值容器使用 FlatDict 二分查找。详见 [索引类型](../architecture/indexes/README.md)。

## 构建与资源所有权

```go
func BuildSegment(w io.Writer, fields Schema, docs []*Document, opts BuildSegmentOptions) error
func BuildSegments(newWriter func(int) (io.Writer, error), fields Schema,
    docs []*Document, opts BuildSegmentsOptions) (int, error)
func NewFullIndexBuilder(opts FullIndexBuildOptions) (*FullIndexBuilder, error)
func NewDeltaIndexBuilder(opts DeltaIndexBuildOptions) (*DeltaIndexBuilder, error)
```

`BuildSegments` 在创建 writer 前校验整个 corpus 的 DocID 唯一性。writer 由调用方管理，包括关闭及错误时删除未完成产物。流式 Full/Delta builder 构造成功后立即 `defer Close()`；`Build()` 成功才把临时代目录原子发布为最终目录。

直接使用 `segment.NewSegmentBuilder` 时：

- `MaxDistinctKeysInMemory <= 0` 关闭溢写，忽略 `TmpDir`。
- 正阈值要求非空 `TmpDir`，其含义是调用方拥有的父目录。每个 builder 在其内创建私有子目录，只清理该子目录。
- `Write()` 成功或失败都会关闭 builder 并清理私有 run；放弃构建时调用 `Close()`。重复 Close 安全；关闭后 AddField/AddRecord/Write 返回 `segment.ErrBuilderClosed`。
- builder 不关闭传入的 writer。阈值按 distinct key 数计，不承诺严格的字节内存上限。

根包选项名统一为 `BuildSegmentOptions`、`BuildSegmentsOptions`、`FullIndexBuildOptions`、`DeltaIndexBuildOptions`。子包保留各自原生名称；根包不提供旧别名。

Full/Delta builder 的 `BuildDirectoryOptions.MaxDistinctKeysInMemory <= 0` 使用 `segment.DefaultMaxDistinctKeysInMemory`，默认值为 1,000,000。此阈值按每个字段 collector 的不同 key 数计算，同一热点 key 累积的 EntryID 仍消耗内存。

## 编码器与存储扩展

`parser.PredicateEncoder` 定义构建、查询之间的编码约定：

```go
type PredicateEncoder interface {
    EncodeConstraint(expr *core.Constraint) ([]parser.EncodedRecord, error)
    EncodeAssignment(value interface{}) ([]parser.EncodedQuery, error)
}
```

`EncodeConstraint` 把一个约束转换为一个或多个容器记录，例如 `city in ["bj", "sh"]` 产生两个 term 记录。构建器将 `EncodedRecord.Record` 与该约束的 EntryID 传给 `IndexBuilder.AddRecord`。`EncodeAssignment` 把查询中一个字段的值转换为容器查询参数，由 `IndexReader.MatchQuery` 返回倒排游标。编码器在 Schema 编译后复用，`EncodeAssignment` 必须支持并发调用。

`NumberTokenizer` 把数值或数值列表转换为十进制整数 term，默认将浮点数截断为整数，用于等值索引；数值区间使用 `RangeEncoder`。自定义编码器和容器示例见 [`container/example`](../../container/example/example.go)。

`segment.FieldMetadata` 是 segment footer 中的字段元数据；`BlockContext.PostingBlock` 是供容器定位 posting 的字节块。`EncodeFlatDict` 和 `EncodeFlatPostingList` 返回编码后的字节，写入目标由调用方负责。

## Reader 与校验

```go
func NewSegmentReader(data []byte) (*SegmentReader, error)
func NewSegmentReaderWithOptions(data []byte, opts SegmentReaderOptions) (*SegmentReader, error)
func OpenSegmentFile(path string, opts OpenSegmentFileOptions) (*SegmentReader, error)
```

`NewSegmentReader` 借用调用方字节切片；Reader 和查询游标存活期间不可修改。`OpenSegmentFile` 对同一文件句柄进行可选整文件校验后 mmap；查询结束后才可 Close。`NewEngine` 成功后接管传入 readers，构造失败时由调用方关闭它们。

`BlockChecksumOnOpen` 是直接打开 Reader 的默认值；`BlockChecksumDisabled` 跳过 payload 哈希，但仍校验 checksum 元数据与结构。未知模式返回错误。当前内置结构校验覆盖块边界、wildcard 标识、字典偏移与字典序、posting 长度/count/padding、AC 结构与跨块引用、range 点表和树拓扑。结构检查不逐条扫描 EntryID，也不替第三方容器验证其私有格式。

所有文件整数使用 little-endian。对齐的 little-endian posting/AC 数组借用原始字节；big-endian 或非对齐输入显式解码。FlatDict、range 等部分元数据会分配 Go heap。

## 检索与 collector

```go
func NewEngine(fields Schema, readers []*SegmentReader) (*Engine, error)
// Engine、CompositeEngine、IndexHolder 均支持：
Retrieve(queries Assignments, opts ...QueryOption) (*BitmapDocSet, error)
RetrieveWithCollector(queries Assignments, collector ResultCollector, opts ...QueryOption) error
RetrieveTopK(queries Assignments, k int, scores ScoreSource, opts ...QueryOption) ([]ScoredDoc, error)
```

`Retrieve` 返回独立集合，可长期持有。`RetrieveWithCollector` 遵循以下契约：

```go
type ResultCollector interface {
    Add(DocID) CollectResult
}
type DuplicateTolerantCollector interface {
    ResultCollector
    AcceptsDuplicates() bool
}
```

普通 collector 每次查询只收到唯一、可见的 DocID；顺序未指定。显式接受重复的 collector 自行去重且不能让重复消耗额外配额，内置 bitmap/Top K 已实现该能力。`CompetitiveCollector` 独立决定是否支持分数剪枝，包装器保留这项能力。

`CollectStop` 终止整个查询，包括尚未执行的 full/delta 层。collector 由单个查询 goroutine 独占，调用方在复用前 Reset。error 返回时流式结果可能不完整，应丢弃或重置。

```go
collector := be_indexer.NewDocIDCollector() // worker 私有，可跨查询复用
collector.Reset()
err := engine.RetrieveWithCollector(assignments, collector)
if err != nil { return err }
result := collector.Bitmap() // 借用，后续 Reset/Add 会修改它
owned := result.Clone()       // 需要长期保留时复制
```

`NewTopKCollector(k, nil)` 返回任意前 k 个不同命中；有分数时返回精确最佳 k 个，收满 k 后继续搜索。`RetrieveTopK(..., nil)` 使用已发布的分数快照；未发布时按零分和 signed DocID 排序，语义不同于无分数 collector。详见 [Top K 设计](../design/topk.md)。

`WithStrictQuery()` 使字段编码/容器错误终止查询。默认跳过失败字段或 lookup，可能漏掉 Include 或绕过 Exclude；未知字段始终忽略。`WithObserver` 支持 `RetrieveObserver`，同时实现 `FieldErrorObserver` 可收到宽松模式下的字段错误。`OnConjunctionMatch` 是各层的 Conjunction 诊断事件，不等于最终去重、合并后的结果数。

Engine 可并发查询；`SetDocVisibilityMask` 及其 mask 必须在发布引擎前配置完成，不可与查询并发修改。`SetScoreSource` 支持原子替换不可变分数快照。

`NewDocVisibilityMask()` 创建全部可见的过滤器；`Hide(docID)` 使该 ID 在使用此 mask 的引擎或 collector 中不可见，`IsVisible(docID)` 查询过滤状态。它不检查文档是否存在，也不修改索引文件。例如较新 delta 更新了 DocID=7，loader 会在较旧 delta 的 mask 中隐藏 7，使查询使用新版本。业务用 `OpenIndex`/`NewIndexHolder` 时由 loader 配置；手动组装引擎时由调用方配置。collector 的 `Reset()` 会清除 mask，复用时需在 Reset 后重新设置。

底层 `FieldCursor.Current()` 与 `PostingIterator.Current()` 都返回当前 EntryID，耗尽时返回 `EndEntryID`，可用 `IsEnd()` 判断。`EntryID.ConjID()` 读取所属子句 ID；`FieldCursors.RemoveExhausted()` 移除排序后末尾的耗尽游标，调用前必须完成排序。

## 快照与热更新

生产加载优先用 `OpenIndex` 或 `NewIndexHolder`，自动核对 schema hash、manifest 与 sidecar。加载模式：

| 模式 | 行为 |
|---|---|
| `SegmentLoadHeapVerify` | 读入 heap，核对大小与整文件 SHA-256 |
| `SegmentLoadMmapVerify` | 默认；同一文件句柄校验大小和整文件 SHA-256 后 mmap |
| `SegmentLoadMmapTrustPublished` | 校验大小与结构，跳过 payload 哈希；仅用于已经可信验证的发布产物 |

`Holder.Reload` 仅在新快照全部加载成功后切换，失败保留旧快照。旧 mmap 等最后一个在途查询退出才回收；不要跨 Reload 使用 `UnsafeCurrent()` 的返回值。退出时关闭 holder。

## 索引合并与全量发布评估

[`api_maintenance.go`](../../api_maintenance.go) 在根包定义类型和实现，供业务评估是否合并 delta、重新构建并发布 full。它只返回建议；不读取索引文件，不执行构建、发布、Reload 或后台调度。业务推荐一个 full + 一个累计 delta，各自可包含多个 segment。调用及后续业务步骤见[最佳实践](best-practices.md#10-索引合并与全量发布评估)。

```go
func CollectIndexMaintenanceStats(m Manifest) IndexMaintenanceStats
func DefaultIndexMaintenancePolicy() IndexMaintenancePolicy
func EvaluateIndexMaintenance(m Manifest, opts IndexMaintenanceOptions) IndexMaintenanceRecommendation
func EvaluateIndexMaintenanceStats(stats IndexMaintenanceStats, opts IndexMaintenanceOptions) IndexMaintenanceRecommendation
```

`IndexMaintenanceRecommendation` 包含 `Decision`、`Stats`、`Reasons`；每个 `IndexMaintenanceReason` 提供 `Code`、`Value`、`Threshold`。`none` 表示无阈值触发，`minor` 建议评估 delta 合并/重建，`major` 建议评估全量重建和发布。若两类条件均满足，只返回 major 及其原因。所有比较均为严格大于，等于阈值不会触发。

对应 Go 常量为 `IndexMaintenanceDecisionNone`、`IndexMaintenanceDecisionRebuildDelta`、`IndexMaintenanceDecisionRebuildFull`。决策的字符串值为 `none`、`minor`、`major`；原因代码使用 `minor_*`、`major_*`。`DeletedToChangedRatio` = `DeletedDocCount / ChangedDocCount`，表示变更计数中的删除比例。

评估结构的 JSON 字段名为：`TimeSinceFullBuild` 使用 `FullAge`，`ChangeCountsAvailable` 使用 `HasExactChangeCounts`，`DeletedToChangedRatio` 使用 `TombstoneRatio`。构建选项中 `MaxDistinctKeysInMemory` 的 JSON 名为 `MaxPostingsInMemory`，`BlockContext.PostingBlock` 的 JSON 名为 `Pl`。

默认参考阈值如下，比例均为计数或字节数之比，不是百分数整数：

| `IndexMaintenancePolicy` 字段 | 默认值 | 对应统计或运行指标 |
| :--- | :--- | :--- |
| `MinorDeltaSegmentThreshold` | 16 | 所有 delta 的 segment 总数 |
| `MinorDeltaDocsRatio` | 0.05 | delta 文档数 / full 文档数 |
| `MinorDeltaBytesRatio` | 0.10 | delta segment 字节数 / full segment 字节数 |
| `MinorChangedDocsRatio` | 0.05 | 变更文档计数 / full 文档数 |
| `MinorP99Ratio` | 1.20 | `FullDeltaP99 / FullOnlyP99` |
| `MajorChangedDocsRatio` | 0.20 | 变更文档计数 / full 文档数 |
| `MajorDeltaBytesRatio` | 0.30 | delta segment 字节数 / full segment 字节数 |
| `MajorTombstoneRatio` | 0.20 | 删除文档计数 / 变更文档计数 |
| `MajorFullAge` | 48 小时 | 距当前 full 构建完成经过的时间 |

`IndexMaintenanceOptions.Policy` 整体为零值时使用默认策略；自定义部分字段应先调用 `DefaultIndexMaintenancePolicy()` 再覆盖。其余未赋值的阈值不会自动补齐，计数/数据比例阈值设置为零也不表示禁用。

`IndexMaintenanceRuntimeObservation` 的指标由业务传入：`FullOnlyP99` 和 `FullDeltaP99` 分别是只查 full、查 full + delta 时请求耗时的第 99 百分位数，应使用可比较的查询负载；两者和 `MinorP99Ratio` 均大于零时才评估延迟比。`TimeSinceFullBuild` 是评估时间减去业务记录的当前 full 构建完成时间，例如 `time.Since(fullBuiltAt)`。库不自动获取时间或指标；未提供 `TimeSinceFullBuild` 时为零，不触发默认时长阈值；`MajorFullAge <= 0` 时禁用此条件。原因代码 `major_full_age` 中的 `Value`、`Threshold` 单位为纳秒。

统计边界：

- `CollectIndexMaintenanceStats` 只遍历 Manifest 描述符，时间复杂度为 O(full 段数 + delta 数量 + delta 段数)，不扫描 posting 或 sidecar，也不校验 Manifest。
- 多个 delta 的文档、变更、删除计数直接累加，未按 DocID 跨 delta 去重，可能重复计数；`ChangeCountsAvailable` 只表示未检测到缺失计数。计数缺失时跳过依赖变更/删除计数的条件。
- 字节数仅包含 segment 描述符的 `Size`；各统计比例的分母为零时记为零。
- `EvaluateIndexMaintenanceStats` 直接使用调用方提供的比例及 `ChangeCountsAvailable`，不会从文档数重新计算比例。

## 性能口径

核心游标循环保持低开销；完整查询仍有编码、迭代器和结果所有权分配。普通 collector 的唯一性保证需要去重集合，内置可接受重复的 collector 避免额外集合。单次查询串行归并各段，Composite 依次执行各层；并行度来自多个查询。评估时同时观察吞吐、P99、分配、RSS、冷启动及构建峰值。
