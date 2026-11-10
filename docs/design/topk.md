# Exact Top K 与独立分数 sidecar

本实现采用与查询无关的 `score(DocID)`：一次查询固定使用一份不可变分数快照，按 **score 降序、signed DocID 升序** 返回最多 N 个不同 Document。这里用 N 表示结果数量，K 始终指 Conjunction 的 Include 数量。

## 先理解对象与术语

检索分成两件事：先确定哪些 Document 满足查询输入，再从满足条件的 Document 中选出排名最高的 N 个。分数不能把一个不满足布尔条件的 Document 变成匹配；它只能帮助跳过那些即使匹配也进不了结果的 Document。

例如一个 Document 表示 `(city 属于 {bj} AND age > 18) OR (tag 属于 {vip})`：它有两个 Conjunction，分别需要 2 个和 1 个 Include，即 K 分别为 2 和 1。查询输入可以是 `Assignments{"city": "bj", "age": 20}`。一个 Conjunction 满足即可让这个 Document 匹配，多个 Conjunction 同时满足也只占一个结果名额。

| 术语 | 本文中的含义 |
| --- | --- |
| Document / Conjunction / DNF | Document 是完整规则；Conjunction 是多个 Predicate 的 AND；多个 Conjunction 之间是 OR，这种表达形式称为 DNF。 |
| Predicate / Constraint / Field | Predicate 是单个条件，由字段 Field 和约束 Constraint 组成。例如 `age > 18` 中，`FieldName` 为 `age`，`Constraint` 描述值 `18`、运算符 `>` 和 Include/Exclude 属性。 |
| Include / Exclude | Include 必须满足；Exclude 命中时否决当前 Conjunction。查询中未提供 Exclude 对应的字段时，不触发该排除条件。 |
| Assignment | 一次查询提供的属性值集合，对应 `Assignments`。 |
| signed DocID / score | DocID 按有符号整数比较，例如 `-3 < -1 < 0 < 2`；score 是外部为整个 Document 提供的排名分数。同分时取更小的 signed DocID。 |
| N / K / M | N 是要返回的结果数，对应 Top K API 的参数 `k`；本文的 K 专指 Conjunction 的 Include 数；M 是整次查询满足布尔条件、可见性和缺分策略的不同 Document 总数。M 通常要完成检索才知道，不能用某条 posting 的长度直接代替。 |
| ConjID / EntryID | ConjID 编码一个 Conjunction 的身份及 K；EntryID 在其基础上增加 Include/Exclude 标志，是倒排条目的排序键。不能把 EntryID 大小当成分数高低。 |
| Posting List / PostingIterator | 按 EntryID 排序的倒排条目及其迭代接口，记录某个查询值能命中哪些条件条目。 |
| FieldCursor / 头部 | 同字段、不同查询值或 segment 的多个 PostingIterator 合并成一个 FieldCursor；“头部”是该游标当前最小的 EntryID。同字段命中多个值仍只算一个字段。 |
| K-group / 前缀 | K 在 EntryID 高位，同 K 的条目自然相邻。本文的前缀具体指 `(K, Conjunction index)`；每个前缀内重新排列 DocID。 |
| wildcard / Z-Entry | K=0 的 Conjunction 使用的特殊条目，总能产生候选，无需满足 Include；命中的 Exclude 仍可以否决它。它的 Document 分数照常从 sidecar 获取。 |

| 检索与打分术语 | 含义及边界 |
| --- | --- |
| 布尔 pivot | 把所有 FieldCursor 按当前 EntryID 排序后，取第 `max(K, 1)` 个游标头部对应的 Conjunction；这里的 K 来自最小头部。它是下一轮检索的候选起点，尚不代表已经匹配。代码变量为 `endConjID`。 |
| SkipTo(target) | 将游标移到第一个 `EntryID >= target` 的位置。目标条目不必存在；已经在 target 或更后面时保持不动。不会逐个调用布尔求值。 |
| galloping search | SkipTo 使用的定位方法：先按指数增大的步长探测，找到目标所在的小区间，再二分查找，适合顺序推进与偶尔的大跨度跳转。 |
| collector / 去重 | 接收已匹配的 DocID 并维护结果的对象。同一 Document 由多个 Conjunction 或 segment 命中时，只保留一个结果。 |
| 最小堆 / 堆根 | 用数组维护的排名结构，根节点是当前保留结果中排名最差的一项，即最低分、同分时最大 signed DocID。替换根节点后局部调整即可，不必每次完整排序。 |
| 建堆 / heapify / 延迟建堆 | 建堆是把已有数组调整为上述结构。延迟建堆指未满 N 时只追加，首次达到 N 个不同有效结果时才自底向上统一建堆。这里的“堆”是数据结构，不是 Go 的内存分配区域。 |
| 阈值 / threshold | 满 N 后的堆根 `(score, DocID)`，新 Document 必须排名优于它才值得替换。未满时没有阈值，不能凭当前最低分拒绝其他有效匹配。 |
| 上界 / bound / block max | 某个区间内可能出现的最好排名。本实现每 64 个已评分 Document 建一个块上界，再建立更大区间的上界树。上界足够高只表示“可能值得检查”，并不证明该区间有布尔匹配。 |
| 剪枝 / pruning | 在已有正确性依据时跳过一段工作：布尔条件证明该段不可能匹配，或者分数上界证明即使匹配也无法进入 Top N。 |
| sidecar / 分数快照 | 独立于布尔索引维护的 DocID→score 数据。本实现是不可变的内存对象；更新时原子替换引用，在途查询仍使用原来的整版数据。 |
| 宽查询 / 窄查询 | 此处指实际匹配很多 / 很少的查询。宽查询通常有更多可由分数剪枝节省的布尔工作；窄查询可能已由普通倒排跳转排除了绝大部分文档。 |

后文还会用到：**full/delta** 是全量索引与增量索引；**可见性过滤** 是排除已删除或被新版替代的 Document；**bitmap** 是用于集合运算的位图；**shard** 是数据分片；**forward matcher** 是给定一个 Document 后直接检查其规则的正向求值器；**mmap** 是把 segment 文件映射到内存读取；**Shadow Testing** 是对同一份原始数据和查询运行参考求值器与优化实现，逐项比较结果。

## 布尔 pivot 如何推进游标

实际链路是：`Assignments → 编码查询值 → 查询各 segment 的 PostingIterator → 同字段合并为 FieldCursor → 按头部 EntryID 排序 → 读取最小头部中的 K → 选择 pivot → 对齐/判定/SkipTo → 重新排序`。

为什么可以跳到 pivot？对 K>0，pivot 之前的任何 Conjunction 最多只能由前 K−1 个字段游标贡献条目，其余游标已经在 pivot 或更后面，凑不齐所需的 K 个 Include。K 又随 EntryID 不减，因此跨 K-group 时也不会漏掉一个所需字段更少的候选。

以下示例固定 K=2、index=0，全部为 Include。数字仅用 DocID 简写对应的 Conjunction：

```text
字段 A：10 → 20 → 40
字段 B：20 → 50
字段 C：30 → 40 → 50
```

| 排序后的头部 | 第 2 个头部，即 pivot | 本轮动作 |
| --- | --- | --- |
| A:10，B:20，C:30 | 20 | 10 只能由 A 贡献，无法凑齐两个字段；把前两个游标 SkipTo(20)。B 已在 20，保持不动。 |
| A:20，B:20，C:30 | 20 | 前两个头部属于同一 Conjunction，且没有命中的 Exclude，20 匹配；将这两个游标推进到 20 之后。 |
| C:30，A:40，B:50 | 40 | 30 只能由 C 贡献，跳到 40。 |
| A:40，C:40，B:50 | 40 | A、C 对齐，40 匹配。 |

真实代码用 `NewEntryID(pivot, false)` 作为对齐目标，以保留 pivot 的 Exclude 条目。同一 Conjunction 的 Exclude 排在 Include 前面；头部对齐后若最小条目为 Exclude，就否决该 Conjunction，并清走其他游标仍指向它的条目。处理完后用 `NewEntryID(pivot, true)+1` 越过该 Conjunction。跳过的不是整个 Document，其其他 Conjunction 仍可命中。K=0 则通过 wildcard 进入候选流程，取 `max(K,1)=1`，并继续检查 Exclude。

分数剪枝接在布尔 pivot 之后：先利用字段数量条件跳过无解区间，再从 pivot 查上界、寻找更远的目标。先从最小头部逐文档检查分数，反而可能破坏原有布尔逻辑已经能做到的长距离跳转。

```text
最小头部 ───────── 布尔 pivot ───────── 分数 target
          布尔条件证明可跳过    分数上界证明可跳过
```

## 论文对应与正确性

[VLDB 09 论文](../../static/design/vldb09-indexing.pdf) 第 5 节在 K-index 的 pivot 处比较上界与第 N 名分数，并允许跳过上界不足的整个 K-index。论文的分数由 Predicate 权重组成；本项目的外部分数直接属于 Document，因此不能套用 Predicate 权重求和，也不能认为 wildcard 的排名分数为零。

本实现保留“布尔 pivot + 安全分数上界”的机制，以独立 `BlockMaxScoreTable` 提供上界。当前 EntryID 的真实顺序为：

```text
bits:   63       56 55       48 47             4 3       0
        +----------+----------+----------------+---------+
        | K (8)    | index(8) | sign + abs(ID) | flags   |
        +----------+----------+----------------+---------+
```

DocID 在每个 `(K, Conjunction index)` 前缀内重新开始排序，非负 ID 在前，负 ID 按绝对值递增在后。因此“跳过剩余 DocID”通常只能跳到下一前缀，不能直接结束整个查询。

检索执行顺序：

1. 求现有 K-group merge 的布尔 pivot，即第 `max(K, 1)` 个 FieldCursor 的头部 Conjunction。
2. K>0 时，pivot 之前不足 K 个字段能参与匹配，且 K 随 EntryID 不减，这一段可以按原算法跳过；K=0 时 pivot 就是最小头部。
3. 已收集结果未满 N 时继续正常匹配并追加，不维护堆、不查询分数上界树；首次收满 N 时统一建堆。此后以堆根 `(score, DocID)` 为阈值，在同一分数快照中寻找 pivot 起下一个可能胜出的 DocID。
4. 将 **所有** FieldCursor 推到该 EntryID，包括 Exclude 和 wildcard；这一整段同时被布尔与分数条件证明无用。
5. 继续原有布尔匹配、liveDocs 检查、full/delta 可见性过滤，最后更新 Top K 堆。

仅当整个分数树都无法胜出时，才可用 `EndEntryID` 结束检索。仅某个前缀的后缀无法胜出时，跳到下一 `(K,index)` 前缀。最后一个前缀显式检查溢出。

上界包含同分时的 signed DocID 排序，不能仅凭 `score <= threshold.score` 丢弃整个同分区间。scored collector 收满 N 个结果也不能返回 `CollectStop`，因为 EntryID 并非分数顺序。

## Sidecar 与发布

`core.NewBlockMaxScoreTable(ids, scores)` 接受按 **signed DocID 严格递增** 排序的并行数组，拒绝重复、越界 ID 和长度不一致。构造器复制输入；NaN 表示缺失，正负无穷是有效分数。

内部按 EntryID 的 DocID 编码顺序保存 key/score，每 64 个已评分 Document 建一个上界，再构建二叉上界树。输入已排序，转换负 ID 顺序和建树均为 O(D)。存储约为每个已评分 Document 16 字节，加上大规模下每 Document 不足 1 字节的上界树；这不包括生产者的输入数组及刷新期间仍被查询持有的旧快照。连续 key 的查分和定位使用 O(1) 偏移，稀疏 key 使用二分。

跳转查询先检查当前块的后缀，随后用上界树拒绝整块；有效叶块至多顺序检查 64 个分数。sidecar 不依赖 segment/container 的物理布局，也无需修改 mmap segment 格式或端序约定。

`BooleanEngine`、`CompositeEngine` 和 `loader.Holder` 均提供：

```go
SetScoreSource(scores core.ScoreSource)
RetrieveTopK(query core.Assignments, n int, scores core.ScoreSource,
    opts ...core.QueryOption) ([]core.ScoredDoc, error)
```

查询显式传入 source 时优先使用它；传 nil 时读取已发布 source，整次查询只读一次。没有发布 source 时，`RetrieveTopK` 将所有文档视为零分，仍返回按 signed DocID 排序的全局 Top N。Holder 的分数独立于索引 Reload，索引更新不会清空分数。

周期任务应先完整获取并验证新分数，成功后只调用一次 `SetScoreSource`。读取失败或构建失败时不发布，保留旧快照。查询不会混用旧分数与新上界。多个并发刷新任务的版本先后顺序由调用方保证；建议串行刷新，或在发布端比较业务版本。

```go
// refresh 由服务已有的 ticker/配置订阅/sidecar 回调触发。
// ids 必须按 signed DocID 严格递增，values 与其一一对应。
func publishScores(h *loader.Holder, ids []core.DocID, values []float64) error {
    next, err := core.NewBlockMaxScoreTable(ids, values)
    if err != nil {
        return err
    }
    h.SetScoreSource(next)
    return nil
}

// 查询只使用进入本次调用时取得的分数快照。
ranked, err := holder.RetrieveTopK(assignments, 100, nil)
```

本次实现的是 SDK 内的 sidecar 构建、原子发布及消费接口；网络协议、文件传输、刷新周期和重试由服务集成层提供，库内没有隐藏的后台刷新任务。普通 `ScoreSource` 仍可使用：它只进行 pivot 对应文档的分数检查，不能认证连续区间。

## 缺失分数、可见性与复用

默认 `MissingScoreZero`：没有分数的文档按零分竞争。sidecar 可以比索引缺少或多出 DocID。当阈值不高于零时，由于无法从稀疏 sidecar 枚举所有缺失 ID，区间剪枝保守停用；这保证负分、有分数缺失、零分平局都不漏结果。高于零时缺失文档无法胜出，可以安全剪枝。

`MissingScoreSkip`：缺分文档不进入结果，可能返回不足 N 个。通过可复用 collector 选择：

```go
// scores 已经初始化。每个查询 worker 独占自己的 collector。
collector := core.NewTopKCollector(100, scores.Snapshot())
collector.SetMissingScoreMode(core.MissingScoreSkip)

// 每次新查询重置，整次查询固定这个 snapshot。
collector.Reset(scores.Snapshot())
err := holder.RetrieveWithCollector(assignments, collector)
ranked := collector.Scored() // 返回独立拥有的排序结果
```

其中 `scores` 为服务持有的 `core.AtomicScoreSource`，首份分数必须在上例调用前发布。`NewTopKCollector(n, nil)` 保留已有的“任意前 N 个命中即停止”契约；它和没有分数时仍做全局 signed DocID 排序的 `RetrieveTopK` 有意区分。

Collector 使用结果数组，以及预分配、回移删除的固定容量 DocID set。数组未满 N 时只追加；首次达到 N 时自底向上建成有类型的最小堆，之后仅保留 N 个赢家。重复 DocID、MissingScoreSkip 下的缺分文档不会触发建堆。被淘汰文档的分数不会在同一次查询中改变，而阈值单调提高，因此不需要记住所有历史命中文档。`Count()` 为当前保留数，最多 N。`Scored()` 排序的是结果副本，未满时调用也不影响后续继续收集；`Reset()` 清空结果后重新进入追加阶段。构造后复用时，Add、去重、统一建堆、堆替换和上界查询不分配内存；完整查询的编码、PostingIterator 初始化和最终结果复制仍有原有分配。

所有 `RetrieveWithCollector` 都按能力适配：普通 collector 由引擎去重；实现 `DuplicateTolerantCollector` 的内置 bitmap/Top K 自行去重；`CompetitiveCollector` 独立控制分数剪枝，无具体类型分支。`CollectStop` 终止后续所有层。

Composite 的 full 和各 delta 流式写入 **同一个 collector**，累计满 N 后统一建堆，后续层共享已经抬高的阈值。full 的 ChangedDocs、delta 的 DeletedDocs 和各 engine 的 liveDocs 在加入 collector 之前生效。旧版本或删除文档不会抬高阈值；sidecar 中包含它们至多让上界偏松，不影响正确性。手工构造 IndexSnapshot 时，仍须遵守已有契约，给旧 delta 设置被较新 delta 覆盖的 liveDocs；loader 已执行此步骤。

## 结果数量与分数分布怎样影响性能

以下讨论的是有分数的精确 Top N；`NewTopKCollector(n,nil)` 的任意前 N 个命中模式仍按原契约提前停止。

```text
结果少于 N：布尔检索 → 可见性过滤 → 去重、打分、追加
首次达到 N：统一建堆，建立第 N 名阈值
已有 N 个：布尔 pivot → 分数剪枝 → 对有效赢家做堆替换
检索结束：对 min(M,N) 个保留结果的副本排序
```

| 场景 | 剪枝收益 | 当前开销与处理 |
| --- | --- | --- |
| N 远小于 M，宽查询，分数与 EntryID 顺序基本独立 | 通常较早建立高阈值，能跳过大量区间 | 前 N 个有效结果只追加，再用 O(N) 统一建堆；后续仅胜出的候选需要 O(log N) 堆调整。 |
| N 接近 M | 阈值建立得晚，剩余可跳过的工作较少 | 大部分时间仍是普通布尔检索、去重和打分；延迟建堆减少收集前 N 项时的堆维护。 |
| N = M | 收到最后一个不同有效 Document 才满，通常没有剩余收益 | 仍执行一次 O(N) 建堆，因为此时无法预知后续不会再有不同匹配；随后返回全部 M 项。 |
| N > M | 始终没有第 N 名阈值，完全不能通过分数阈值剪枝 | 始终只追加，不建堆、不查询分数上界树；仍需完成布尔检索、去重、打分及最终排序。 |
| 高分沿 EntryID 单调靠后 | 很多后来文档都能胜出，难以跳过 | 满 N 后仍会反复替换堆根；延迟建堆只改善最初填充阶段。可比较关闭分数剪枝后的开销。 |
| 窄查询 | 普通布尔 pivot 已跳过大量文档，额外收益可能很小 | N 大于命中量时使用追加阶段；若已满但上界帮助很小，可选择 `WithScorePruning(false)`。 |
| 默认缺分零分，阈值不高于零 | 稀疏 sidecar 的区间剪枝保守停用 | 仍按零分规则维护精确排名，不能以缺分为由丢弃可能胜出的文档。 |

延迟建堆将“前 N 个结果逐个维护堆”的最坏 O(N log N) 调整，改为追加加一次 O(N) 建堆。N>M 时，结果数组的追加工作为 O(M)，不再有堆调整；查分、去重、布尔检索成本需另外计算。按排名返回全部命中仍需要打分，以及 `Scored()` 的 O(M log M) 排序和结果副本分配。这些成本相对于只返回无序命中集合的 `Retrieve` 仍然存在。

**延迟建堆没有改变容量策略。** 构造器仍按请求 N 预分配结果数组和固定容量去重表，内存为 O(N)；复用时 `Reset()` 仍会清空整个去重表，成本也是 O(N)。所以请求 Top 1000000、实际只命中 1000 时，虽然不会建堆，预分配与清空的成本仍可能明显。当前没有按命中量自动缩容或按需扩容；容量优化需要单独权衡首次查询分配与后续复用。关闭分数剪枝也不会消除这些容量成本。

“均匀随机分数下加速很大”还要求高分文档有机会较早满足布尔条件。以独立均匀分数为例，阈值达到 0.999 后，64 个 Document 全部低于阈值的概率约为 `0.999^64 ≈ 93.8%`，很多块可以整体跳过；若高分文档大多不匹配，它们就不能进入堆、抬高阈值。N 接近或超过 M 时更无法套用小 N 的加速比。

## 路径选择与后续方案

单一策略无法覆盖所有数据分布。`WithScorePruning(false)` 可关闭分数跳转，保留准确的全局 Top K 和原有布尔跳转；低层等价选项为 `ctx.DisableScorePruning = true`。关闭后 collector 仍打分，并按上述未满追加、首次满时建堆的流程维护结果。

| 路径 | 最有利的场景 | 代价与正确性要求 | 当前状态 |
| --- | --- | --- | --- |
| 布尔 pivot + sidecar 区间上界 | 宽查询、小 N，能较早形成高阈值 | 单次查询固定分数；前缀边界、缺失分数保守处理 | 已实现 |
| 普通布尔跳转 + 有界堆 | 极窄查询；高分沿 EntryID 一直递增 | 枚举全部布尔命中；不会因收满 N 而提前停止 | 已实现，可关闭分数剪枝 |
| 按全局分数顺序验证 Document | 分数与查询无关，命中概率较高，N 很小 | 需要可随机访问的 forward matcher，按同一快照的 score/DocID 顺序验证；得到 N 个有效匹配即可精确停止 | 建议下一阶段验证 |
| 按上界优先访问 DocID 块 | 高分在当前 EntryID 的后半部，或分数聚集在少数块 | 为乱序块建立独立/可定位的 posting 游标；只能在剩余所有块上界不优于阈值时结束 | 设计候选 |
| 分数分层 bitmap + 布尔过滤 | Document 很多、周期更新、可接受额外 bitmap 内存 | 从高分层往低分层搜索；边界层必须按精确分数和 DocID 完整处理；过滤器不能作为额外 Include 改变 K | 设计候选 |
| 每 shard/layer 精确 Top N 再全局合并 | 多核或分布式检索、层间可并行 | 每层先做可见性过滤、去重并返回真正 Top N；全局去重；动态共享阈值必须只由真实有效赢家形成 | 当前实现为串行共享堆 |

**优先探索全局分数顺序 + forward matcher。** 对独立于分数的匹配概率 p，找到 N 个命中平均需要验证约 N/p 个 Document（仅为独立分布估计）。它不受 EntryID 顺序下“高分来得太晚”的限制。例如 N=100、p=10% 时，约验证 1000 个 Document 就可能完成；p=0.01% 时则约需 100 万个，倒排查询往往更好。

该路径的全局分数顺序还必须覆盖本次可见的全部 DocID：默认缺失零分的文档也要放入排名，或者明确采用 MissingScoreSkip；仅遍历稀疏 sidecar 中的已评分 ID 无法保证默认语义。

当前只有 `oracle.EvaluateDocument` 提供基于原始规则的参考求值，它含解析和分配，不适合作为线上 forward matcher。下一阶段应由 builder 输出不可变、预编码的 forward Document 数据，复用与索引相同的 Constraint 语义，并建立原始规则、倒排、forward 三方 Shadow Testing。score 排序快照与 forward 版本必须配合 liveDocs/ChangedDocs 去除旧版本；不能先验证旧版本再在返回时删除，否则会导致不足 N 或错误阈值。

**按块上界优先访问** 是另一个值得验证的折中：在 EntryID 的各前缀内，以 sidecar 的块最高分建立优先队列，从高上界块开始检索，尽早建立阈值。现有 PostingIterator 只能单调 SkipTo，不能直接对同一个 cursor 按分数乱序访问，所以需要受块区间限制的新游标或独立 seek 句柄。必须测量额外随机 mmap 访问、缓存局部性和每块初始化成本。

**自适应路由** 应建立在数据测量上：统计查询的 posting 规模、实际匹配率、达到 N 个赢家前的工作量、score skip 距离和更新成本，再在以上路径间选择。切换可以从空堆重新跑，或携带已经验证的赢家；不能只凭采样分数设定剪枝阈值。现阶段提供显式开关，没有写入未经工作负载验证的自动选择阈值。

## 验证与复现

- `engine/topk_test.go`：400 个带多 Conjunction、Include/Exclude、wildcard、range 的 Document，拆分为多个 segment；240 个固定种子随机查询，比较原始 DNF、普通检索、普通 ScoreSource 与 BlockMaxScoreTable，两种缺分策略、多种 N。
- `core/topk_exact_test.go`：负 ID、极值 ID、NaN/无穷、同分、重复/淘汰后再输入、复用；1200 次随机区间认证，覆盖 K/index 为 0/1/254/255 的边界；热路径分配检查。
- 延迟建堆验证：未满时不调用上界，首次满时立即提供正确阈值；重复/缺分不占名额，未满时读取结果不改变后续收集；覆盖 N=0/1、N=M、N>M，以及 Reset 后再次建堆。追加、建堆和替换路径的分配检查均为 0。
- engine/composite/holder：晚到高分、跨层去重、更新后不匹配、删除、旧 delta 遮蔽、查询中替换分数、并发发布、mmap Reload 后分数保留。
- `engine/topk_bench_test.go`：10 万 Document，四个 Include 字段，命中率 100%/10%/0.1%，高分靠前/随机/靠后，N=10/100。每个基准先检查结果与准确的全量枚举一致，再计时；collector 复用，包含查询编码与游标初始化，不含结果复制和排序。
- `core/topk_fill_bench_test.go`：固定 4096 个不同有效 Document，N=512/4096/16384，比较递增/递减/随机分数下的收集成本，专门覆盖满堆前后及始终未满的情况。

```bash
make test
make build
go vet ./...
go test -race ./core ./engine ./loader
go test ./engine -run '^$' -bench '^BenchmarkTopK$' -benchmem -benchtime=200ms
go test ./core -run '^$' -bench '^BenchmarkTopKFill$' -benchmem -benchtime=200ms -count=3
```

`matches/op` 是到达 OnConjunctionMatch 的 Conjunction 数量，不是全部 merge 迭代次数或 SkipTo 次数。真实流量仍需验证分数与匹配率的相关性、不同 K/index 的分布、全量 sidecar 重建耗时、刷新峰值内存和冷 mmap 页访问。

## 本机基准记录

以下是延迟建堆改动前的查询基准，用于展示不同检索场景的差异；延迟建堆自身的前后对比见后面的收集阶段基准。2026-11-09，Apple M3 Pro / darwin arm64，`-benchtime=200ms` 单轮合成基准，10 万 Document，Top 10。时间单位为微秒。`full_scan` 使用 DenseScoreTable 并隐藏 CompetitiveCollector 接口，完整遍历布尔命中；`score_check` 使用同一 DenseScoreTable 在 pivot 检查单文档；`block_max` 使用独立分块 sidecar。三条路径都先验证结果相同。

| 命中率 | 分数顺序 | full_scan μs | score_check μs | block_max μs | block_max 的 OnConjunctionMatch 次数 |
| --- | --- | ---: | ---: | ---: | ---: |
| 100% | 高分靠前 | 16643.80 | 19977.28 | 3.77 | 10 |
| 100% | 随机 | 16936.75 | 16921.72 | 52.71 | 107 |
| 100% | 高分靠后 | 20927.17 | 22619.67 | 22195.38 | 100000 |
| 10% | 高分靠前 | 3431.71 | 2069.34 | 5.24 | 10 |
| 10% | 随机 | 3272.14 | 2101.91 | 253.59 | 76 |
| 10% | 高分靠后 | 4817.00 | 4235.15 | 4052.73 | 10000 |
| 0.1% | 高分靠前 | 36.95 | 25.79 | 5.50 | 10 |
| 0.1% | 随机 | 39.32 | 33.28 | 35.97 | 37 |
| 0.1% | 高分靠后 | 41.08 | 45.62 | 44.93 | 100 |

宽查询随机分数时，Top 10 的布尔匹配回调从 100000 次降至 107 次；高分单调靠后时仍需验证所有命中，分数树通常只有额外开销。0.1% 命中率下，原有布尔 pivot 已能跨越大量文档，收益显著收窄。不要把宽查询的加速比外推到全部流量。

这些用例均为 38 allocs/op，通常约 832 B/op，来自已有查询初始化；分数查找和 collector 的逐候选热路径没有新增分配。单轮时间会受本机负载/频率影响；部署选择应以实际业务数据的分布、分位延迟及 sidecar 更新预算为准。

## 延迟建堆的收集阶段基准

同一台 Apple M3 Pro / darwin arm64，固定 M=4096、DenseScoreTable，分别在本次修改前后执行 `BenchmarkTopKFill`，每项 `-benchtime=200ms -count=3`，下表取三次耗时中位数，单位为微秒。包含 Reset、查分、去重与收集；不包含 collector 构造、布尔检索、分数上界查询或最终 Scored() 的复制排序。所有用例均为 0 B/op、0 allocs/op，结果先与完整排序对照。

| 分数输入顺序 | 请求 N | 与命中量的关系 | 修改前 μs | 延迟建堆 μs | 耗时变化 |
| --- | ---: | --- | ---: | ---: | ---: |
| 递增 | 512 | N < M | 217.35 | 202.88 | -6.7% |
| 递增 | 4096 | N = M | 46.56 | 46.81 | +0.5% |
| 递增 | 16384 | N > M | 42.06 | 37.20 | -11.6% |
| 递减 | 512 | N < M | 40.13 | 34.90 | -13.0% |
| 递减 | 4096 | N = M | 122.29 | 56.26 | -54.0% |
| 递减 | 16384 | N > M | 110.27 | 37.28 | -66.2% |
| 随机 | 512 | N < M | 71.87 | 71.83 | -0.1% |
| 随机 | 4096 | N = M | 51.21 | 57.61 | +12.5% |
| 随机 | 16384 | N > M | 54.43 | 37.04 | -31.9% |

N>M 时三个输入分布都减少了收集耗时，因为从未建堆；N=M 的随机分数用例反而慢约 12.5%，说明一次性建堆的常数、原先逐项插入的实际调整量也会影响结果。N<M 时，满堆之后的替换工作仍然存在，不能把填充阶段的改善等同于整个查询同比例加速。尤其在窄查询和超大 N 下，还需计入本基准已包含的 O(N) Reset，以及未计入的构造预分配和最终排序成本。
