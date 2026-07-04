# 广告定向索引库 v2 任务拆解

## 已完成

- [x] 冻结 64-bit 编码继续使用的设计基线。
- [x] 冻结 exclude-on-present-value 语义。
- [x] 冻结 manifest + 多 segment 产物模型。
- [x] 冻结 full + delta 查询公式：`(FullResult - ChangedDocs) ∪ DeltaResult`。
- [x] 明确不能用单个全局 LiveDocs 同时过滤 full/delta。

## 本阶段实现

- [x] 方案文档：proposal/design/tasks。
- [x] `CompositeEngine / IndexSnapshot` 基础查询合并层。
- [x] manifest 数据结构与基础校验。
- [x] 64-bit 构建期边界校验。
- [x] PostingList `uint32` count 防溢出。
- [x] 单元测试：exclude missing、full+delta update/delete/recreate、64-bit 边界。

## 下一阶段

- [x] manifest 文件读写、checksum 与 CURRENT 原子切换。
- [x] wildcard sidecar 持久化格式。
- [x] changed_docs/deleted_docs sidecar 持久化格式。
- [x] Loader / Snapshot atomic reload。
- [x] Oracle evaluator 与语义单测。
- [x] full/delta 构建目录生成工具。
  - [x] `BuildFullIndexDir`：从 full docs 生成 generation 目录、segment descriptor 与 wildcards sidecar。
  - [x] `BuildDeltaIndexDir`：从 mutations 生成 delta 目录、changed/deleted sidecar 与可选 delta segment。
  - [x] `NewSnapshotManifest`：组装可发布 manifest 并复用 `Manifest.Validate`。
  - [x] 支持 delete-only delta：delta segments 允许为空，但 changed_docs 必须存在。
  - [x] 构建目录使用 tmp -> rename 发布，避免 loader 看到半成品。
  - [x] 端到端测试：构建 full/delta -> PublishManifest -> loader.OpenIndex -> 查询验证。
- [x] 真正流式 full 构建。
  - [x] `DocumentIterator` / `BuildFullIndexDirFromIterator`，避免业务持有全量 documents。
  - [x] `segment.ExternalBuilder` 使用 external sort runs，避免单 segment 全量 posting 驻留内存。
  - [x] segment 文件直接写临时文件，完成后流式 sha256，不再整体 bytes buffer。
  - [x] wildcard sidecar 使用 external-sort runs，避免 K=0 文档导致全量 wildcard entries 驻留内存。
  - [x] run merge 限制同时打开文件数量，避免 FD 上限。
  - [x] 流式构建端到端测试、跨 run merge 正确性测试。
  - [x] AC matcher 兼容测试。
- [x] compact 策略自动化。
  - [x] delta descriptor 增加 changed/deleted count，构建时填充。
  - [x] `compact` 包提供 manifest 静态统计、默认阈值、none/minor/major 决策与原因。
  - [x] 支持可选 p99/full age 观测输入。
  - [x] 修复多 delta update/update 正确性：较老 delta engine 使用后续 changed_docs live filter。
  - [x] compact policy 单测与多 delta update/update loader 端到端测试。
- [x] full/delta benchmark。
- [x] Segment v2：Z-list 内置、schema hash 内置、block checksum。
  - [x] v2 magic/metadata/schema hash/wildcards block/block checksum。
  - [x] reader 仅支持 v2，加载时校验 block checksum。
  - [x] Builder 与 ExternalBuilder 均支持 v2。
  - [x] loader 支持 sidecar 缺失时读取 segment 内置 Z-list。
  - [x] v2 metadata、wildcards、checksum mismatch 单测。
  - [x] manifest format 与物理 segment version 一致性校验。
