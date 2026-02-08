# BE Indexer 增量索引构建技术改造方案

## 1. 背景与目标

### 1.1 场景
广告检索场景，广告总量大（百万级），但更新率较低（5-10%）。每次全量构建索引成本高，需要支持增量构建以提升性能。

### 1.2 目标
- 支持 Document 级别的构建缓存
- 未变更的 Document 直接复用上次构建的中间结果
- 最小化代码改动，保持向后兼容

## 2. 架构设计原则

### 2.1 Holder 自治原则
- Parser 由 Holder 内部决定，不暴露给上层
- 用户通过注册机制在库初始化时自定义 Holder 及其 Parser

### 2.2 原始值存储原则  
- DefaultEntriesHolder 直接存储原始字符串值作为 Term key
- 不再强制转换为 uint64 ID
- 接受内存占用增加的代价换取简单性和缓存稳定性

### 2.3 显式缓存控制原则
- 库提供 Cache 接口，由业务决定是否启用及如何实现
- 业务通过注入方式提供 Cache 实现（如 Redis、内存、文件等）

### 2.4 Version 语义原则
- Document 增加 Version 字段，由业务层提供版本标识
- Version 变化代表 Document 内容变化，触发重新构建

## 3. 技术方案

### 3.1 Document 结构增强

```go
// document.go
type Document struct {
    ID      DocID          `json:"id"`
    Version uint64         `json:"version"`  // 【新增】业务提供的版本号
    Cons    []*Conjunction `json:"cons"`
}

func NewDocument(id DocID) *Document {
    return &Document{
        ID:      id,
        Version: 0,  // 默认为0，表示不使用缓存
        Cons:    make([]*Conjunction, 0),
    }
}
```

### 3.2 DefaultEntriesHolder 改造

```go
// entries_holder.go
type Term struct {
    FieldID uint64
    // 【修改】从 uint64 改为 string，直接存储原始值
    Value   string  
}

type DefaultEntriesHolder struct {
    debug     bool
    maxLen    int64
    avgLen    int64
    plEntries map[Term]Entries

    // 【删除】不再暴露 Parser 配置
    // Parser      parser.FieldValueParser
    // FieldParser map[BEField]parser.FieldValueParser
    
    // 【可选】内部保留，用于特殊需求
    valueParser func(interface{}) ([]string, error)
}

// 【修改】IndexingBETx 直接处理原始值
func (h *DefaultEntriesHolder) IndexingBETx(field *FieldDesc, bv *BoolValues) (TxData, error) {
    // 直接将值转为字符串，不分配 ID
    strValues := convertToStrings(bv.Value)
    return &StringTxData{Values: strValues}, nil
}

// StringTxData 替代 Uint64TxData
type StringTxData struct {
    Values []string
}
```

### 3.3 Cache 接口设计

**设计原则**：通过接口抽象实现缓存恢复，避免类型断言

```go
// doc_cache.go

// DocCacheKey 缓存键
type DocCacheKey struct {
    DocID   DocID
    Version uint64
}

// DocCacheEntry 文档级缓存条目
type DocCacheEntry struct {
    DocID       DocID
    Version     uint64
    SchemaHash  uint64              // 字段配置哈希，用于校验
    ConjEntries []*ConjCacheEntry   // Conjunction 级别缓存
}

// ConjCacheEntry Conjunction 缓存
type ConjCacheEntry struct {
    Index       int
    Size        int
    WildcardEID EntryID
    FieldEntries []*FieldCacheEntry
}

// FieldCacheEntry 字段级别缓存
type FieldCacheEntry struct {
    Field   BEField
    // 【修改】存储序列化后的 TxData，而非具体值
    // 这样恢复时可以通过 holder.DecodeTxData() 解码，保持接口抽象
    TxDataList []TxCacheEntry
}

// TxCacheEntry 单个表达式缓存
type TxCacheEntry struct {
    EID       EntryID  // EntryID 包含 ConjID + Incl/Excl 信息
    DataBytes []byte   // 序列化后的 TxData（由 holder.Encode 生成）
}

// DocLevelCache 缓存接口，由业务实现
type DocLevelCache interface {
    // Get 获取缓存
    Get(key DocCacheKey) (*DocCacheEntry, bool)
    
    // Set 设置缓存
    Set(key DocCacheKey, entry *DocCacheEntry)
    
    // Clear 清空缓存（Schema 变化时调用）
    Clear()
}

// 设计说明：
// 1. 缓存存储的是序列化后的 TxData（DataBytes），而非具体的值或 ID
// 2. 恢复时使用 holder.DecodeTxData() 解码，由 Holder 自己决定如何解析
// 3. 这种方式保持了 EntriesHolder 接口的抽象，支持任意 Holder 实现
// 4. 不同 Holder 可以有自己的 TxData 格式，通过 Encode/Decode 接口处理
```

### 3.4 IndexerBuilder 集成缓存

```go
// index_builder.go

type IndexerBuilder struct {
    BuilderOption
    indexer     BEIndex
    fieldsData  map[BEField]*FieldDesc
    idAllocator parser.IDAllocator
    
    // 【新增】文档级缓存
    docCache    DocLevelCache
    schemaHash  uint64  // 当前字段配置哈希
}

// 【新增】Builder 选项
func WithDocLevelCache(cache DocLevelCache) BuilderOpt {
    return func(builder *IndexerBuilder) {
        builder.docCache = cache
    }
}

// 【修改】buildDocEntries 支持缓存
func (b *IndexerBuilder) buildDocEntries(doc *Document) error {
    // 1. 检查是否需要缓存
    if b.docCache != nil && doc.Version > 0 {
        cacheKey := DocCacheKey{DocID: doc.ID, Version: doc.Version}
        if cached, ok := b.docCache.Get(cacheKey); ok && cached.SchemaHash == b.schemaHash {
            // 缓存命中且 Schema 匹配，直接恢复
            return b.restoreFromCache(doc, cached)
        }
    }
    
    // 2. 缓存未命中，正常构建
    result := b.buildAndCapture(doc)
    
    // 3. 保存到缓存
    if b.docCache != nil && doc.Version > 0 {
        cacheKey := DocCacheKey{DocID: doc.ID, Version: doc.Version}
        b.docCache.Set(cacheKey, result)
    }
    
    return nil
}

// 【新增】从缓存恢复
// 【修正】通过接口方法恢复，避免类型断言
func (b *IndexerBuilder) restoreFromCache(doc *Document, cached *DocCacheEntry) error {
    for _, conjCache := range cached.ConjEntries {
        conjID := NewConjID(doc.ID, conjCache.Index, conjCache.Size)
        
        // 恢复 Wildcard
        if conjCache.WildcardEID != 0 {
            b.indexer.addWildcardEID(conjCache.WildcardEID)
        }
        
        // 恢复各字段 - 通过接口抽象，不依赖具体 Holder 类型
        for _, fieldCache := range conjCache.FieldEntries {
            desc := b.fieldsData[fieldCache.Field]
            container := b.indexer.newContainer(conjID.Size())
            holder := container.CreateHolder(desc)
            
            // 使用接口方法恢复，不依赖具体实现
            for _, txCache := range fieldCache.TxDataList {
                // 解码 TxData
                txData, err := holder.DecodeTxData(txCache.DataBytes)
                if err != nil {
                    return fmt.Errorf("decode tx data failed: %v", err)
                }
                
                // 构造 IndexingBETx 并提交
                tx := IndexingBETx{
                    field:  desc,
                    holder: holder,
                    EID:    txCache.EID,
                    Data:   txData,
                }
                if err := holder.CommitIndexingBETx(tx); err != nil {
                    return fmt.Errorf("commit tx failed: %v", err)
                }
            }
        }
    }
    return nil
}
        
        // 恢复各字段
        for _, fieldCache := range conjCache.FieldEntries {
            desc := b.fieldsData[fieldCache.Field]
            container := b.indexer.newContainer(conjID.Size())
            holder := container.CreateHolder(desc)
            
            // 直接提交原始值
            for i, rawValue := range fieldCache.RawValues {
                entryID := fieldCache.EntryIDs[i]
                term := Term{FieldID: desc.ID, Value: rawValue}
                holder.(*DefaultEntriesHolder).plEntries[term] = 
                    append(holder.(*DefaultEntriesHolder).plEntries[term], entryID)
            }
        }
    }
    return nil
}

// 【新增】构建并捕获结果
func (b *IndexerBuilder) buildAndCapture(doc *Document) *DocCacheEntry {
    result := &DocCacheEntry{
        DocID:      doc.ID,
        Version:    doc.Version,
        SchemaHash: b.schemaHash,
    }
    
    // 遍历所有 Conjunction 构建并捕获
    for idx, conj := range doc.Cons {
        size := conj.CalcConjSize()
        conjID := NewConjID(doc.ID, idx, size)
        
        conjCache := &ConjCacheEntry{
            Index: idx,
            Size:  size,
        }
        
        // 处理 wildcard
        if size == 0 {
            wildcardEID := NewEntryID(conjID, true)
            b.indexer.addWildcardEID(wildcardEID)
            conjCache.WildcardEID = wildcardEID
        }
        
        // 构建每个字段并捕获 TxData
        for field, exprs := range conj.Expressions {
            fieldCache := &FieldCacheEntry{
                Field: field,
            }
            
            desc := b.createFieldData(field)
            container := b.indexer.newContainer(size)
            holder := container.CreateHolder(desc)
            
            for _, expr := range exprs {
                // 生成 TxData
                txData, err := holder.IndexingBETx(desc, expr)
                if err != nil {
                    continue
                }
                
                // 序列化 TxData 用于缓存
                dataBytes, err := txData.Encode()
                if err != nil {
                    continue
                }
                
                entryID := NewEntryID(conjID, expr.Incl)
                fieldCache.TxDataList = append(fieldCache.TxDataList, TxCacheEntry{
                    EID:       entryID,
                    DataBytes: dataBytes,
                })
                
                // 提交到 Holder
                tx := IndexingBETx{
                    field:  desc,
                    holder: holder,
                    EID:    entryID,
                    Data:   txData,
                }
                holder.CommitIndexingBETx(tx)
            }
            
            if len(fieldCache.TxDataList) > 0 {
                conjCache.FieldEntries = append(conjCache.FieldEntries, fieldCache)
            }
        }
        
        result.ConjEntries = append(result.ConjEntries, conjCache)
    }
    
    return result
}
```

### 3.5 Schema 版本管理

```go
// index_builder.go

// calcSchemaHash 计算字段配置哈希
func (b *IndexerBuilder) calcSchemaHash() uint64 {
    h := fnv.New64a()
    for field, desc := range b.fieldsData {
        h.Write([]byte(field))
        h.Write([]byte(desc.Container))
        // 可扩展：包含更多配置项
    }
    return h.Sum64()
}

// ConfigField 配置字段时更新 Schema hash
func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption) {
    // ... 原有逻辑 ...
    
    // 【新增】更新 Schema hash
    b.schemaHash = b.calcSchemaHash()
    
    // 【新增】Schema 变化时清空缓存
    if b.docCache != nil {
        b.docCache.Clear()
    }
}
```

## 4. 任务拆分

### Phase 1: 基础改造（最小改动）

#### Task 1.1: Document 添加 Version 字段
- **文件**: `document.go`
- **改动**: 
  - 在 Document 结构体添加 `Version uint64` 字段
  - 修改 `NewDocument` 初始化 Version 为 0
- **影响**: 向后兼容，Version=0 表示不使用缓存
- **预计工时**: 0.5 天

#### Task 1.2: Term 结构体改造
- **文件**: `entries_holder.go`, `id_types.go`
- **改动**:
  - Term.IDValue 从 uint64 改为 string
  - 同步修改所有使用 Term 的地方
- **影响**: 需要同步修改 roaringidx 等模块
- **预计工时**: 1 天

#### Task 1.3: DefaultEntriesHolder 简化
- **文件**: `entries_holder.go`
- **改动**:
  - 删除 Parser 和 FieldParser 字段
  - IndexingBETx 直接返回原始字符串值
  - 新增 StringTxData 类型
- **影响**: 删除现有功能，需确认无业务依赖
- **预计工时**: 1 天

### Phase 2: 缓存机制实现

#### Task 2.1: Cache 接口定义
- **文件**: `doc_cache.go` (新增)
- **改动**:
  - 定义 DocCacheKey, DocCacheEntry 等结构体
  - 定义 DocLevelCache 接口
- **影响**: 新增文件，无侵入性
- **预计工时**: 0.5 天

#### Task 2.2: IndexerBuilder 集成缓存
- **文件**: `index_builder.go`
- **改动**:
  - 添加 docCache 字段和 WithDocLevelCache 选项
  - 修改 buildDocEntries 支持缓存检查
  - 实现 restoreFromCache 和 buildAndCapture
- **影响**: 核心逻辑改动，需充分测试
- **预计工时**: 2 天

#### Task 2.3: Schema 版本管理
- **文件**: `index_builder.go`
- **改动**:
  - 添加 schemaHash 字段
  - 实现 calcSchemaHash
  - ConfigField 时更新 hash 并清空缓存
- **影响**: 与现有逻辑集成
- **预计工时**: 0.5 天

### Phase 3: 测试与验证

#### Task 3.1: 单元测试
- **文件**: `doc_cache_test.go`, `index_builder_test.go`
- **内容**:
  - 测试缓存命中/未命中场景
  - 测试 Schema 变化时缓存失效
  - 测试 Version=0 时不使用缓存
- **预计工时**: 1 天

#### Task 3.2: 集成测试
- **文件**: 新增测试文件或 example
- **内容**:
  - 模拟广告场景：10万文档，10%更新
  - 对比全量构建 vs 增量构建性能
  - 验证检索结果正确性
- **预计工时**: 1 天

#### Task 3.3: 内存和性能测试
- **内容**:
  - 测试缓存内存占用
  - 测试恢复速度
  - 对比改造前后的整体性能
- **预计工时**: 0.5 天

### Phase 4: 示例和文档

#### Task 4.1: 使用示例
- **文件**: `example/incremental_indexer/main.go`
- **内容**:
  - 展示如何配置和使用缓存
  - 展示内存缓存的简单实现
- **预计工时**: 0.5 天

#### Task 4.2: 技术文档
- **文件**: `docs/incremental_building.md`
- **内容**:
  - 设计原理
  - 使用指南
  - 最佳实践
- **预计工时**: 0.5 天

## 5. 实施计划

| 阶段 | 任务 | 工时 | 依赖 |
|-----|------|-----|------|
| Phase 1 | Task 1.1 - 1.3 | 2.5 天 | 无 |
| Phase 2 | Task 2.1 - 2.3 | 3 天 | Phase 1 |
| Phase 3 | Task 3.1 - 3.3 | 2.5 天 | Phase 2 |
| Phase 4 | Task 4.1 - 4.2 | 1 天 | Phase 3 |
| **总计** | | **9 天** | |

## 6. 关键设计决策

### 6.1 为什么通过 TxData 序列化实现缓存恢复？

**问题**：直接在缓存中存储原始值（如 `RawValues []string`）会导致恢复时需要类型断言，破坏接口抽象。

**错误做法（已废弃）**：
```go
// ❌ 错误：类型断言破坏了接口抽象
holder.(*DefaultEntriesHolder).plEntries[term] = entryID
```

**正确做法（当前方案）**：
```go
// ✅ 正确：通过接口方法恢复，保持抽象
for _, txCache := range fieldCache.TxDataList {
    txData, err := holder.DecodeTxData(txCache.DataBytes)  // 接口方法
    tx := IndexingBETx{field: desc, holder: holder, EID: txCache.EID, Data: txData}
    holder.CommitIndexingBETx(tx)  // 接口方法
}
```

**优势**：
1. **接口抽象保持**：不依赖具体 Holder 类型，支持任意 EntriesHolder 实现
2. **扩展性**：新增 Holder 类型时无需修改缓存逻辑，只需实现 Encode/Decode
3. **一致性**：恢复流程与正常构建流程一致，都通过 CommitIndexingBETx
4. **类型安全**：编译期检查，无运行时类型断言风险

### 6.2 Holder 与 Parser 的关系

**当前架构**：
- Holder 内部决定 Parser，不对外暴露
- 通过 `EntriesHolder.IndexingBETx()` 接口隐藏解析细节
- 缓存存储序列化后的 TxData，而非原始值或 ID

**好处**：
- DefaultEntriesHolder 可以使用递增 ID（内存紧凑）
- ACHolder 可以直接存储字符串（无需 ID 转换）
- RangeHolder 可以存储范围对象
- 每种 Holder 自行决定存储格式，通过 Encode/Decode 统一接口

## 8. 风险与应对

| 风险 | 影响 | 应对策略 |
|-----|------|---------|
| Term 改为 string 后内存增加 | 高 | 测试内存占用，如无法接受需回退或优化 |
| 缓存数据损坏导致检索错误 | 高 | 添加 Schema hash 校验、Version 校验、可选 CRC |
| 并发构建时的竞态条件 | 中 | 构建期单线程，或 Cache 实现加锁 |
| 向后兼容性 | 中 | 保持 Version=0 时行为不变 |
| 接口抽象失效 | 低 | 【已规避】通过 TxData 序列化机制，避免类型断言 |

## 9. 接口示例

### 9.1 业务侧使用示例

```go
package main

import (
    "github.com/echoface/be_indexer"
)

// 内存缓存实现示例
type MemoryDocCache struct {
    data map[be_indexer.DocCacheKey]*be_indexer.DocCacheEntry
}

func NewMemoryDocCache() *MemoryDocCache {
    return &MemoryDocCache{
        data: make(map[be_indexer.DocCacheKey]*be_indexer.DocCacheEntry),
    }
}

func (c *MemoryDocCache) Get(key be_indexer.DocCacheKey) (*be_indexer.DocCacheEntry, bool) {
    entry, ok := c.data[key]
    return entry, ok
}

func (c *MemoryDocCache) Set(key be_indexer.DocCacheKey, entry *be_indexer.DocCacheEntry) {
    c.data[key] = entry
}

func (c *MemoryDocCache) Clear() {
    c.data = make(map[be_indexer.DocCacheKey]*be_indexer.DocCacheEntry)
}

func main() {
    // 创建带缓存的 Builder
    cache := NewMemoryDocCache()
    builder := be_indexer.NewIndexerBuilder(
        be_indexer.WithDocLevelCache(cache),
    )
    
    // 配置字段
    builder.ConfigField("age", be_indexer.FieldOption{
        Container: be_indexer.HolderNameDefault,
    })
    builder.ConfigField("city", be_indexer.FieldOption{
        Container: be_indexer.HolderNameDefault,
    })
    
    // 添加文档（带 Version）
    for _, ad := range ads {
        doc := be_indexer.NewDocument(be_indexer.DocID(ad.ID))
        doc.Version = uint64(ad.UpdateTime.Unix())  // 业务提供版本
        
        conj := be_indexer.NewConjunction().
            In("age", ad.TargetAge).
            In("city", ad.TargetCities)
        doc.AddConjunction(conj)
        
        builder.AddDocument(doc)
    }
    
    // 构建索引
    indexer := builder.BuildIndex()
    
    // 下次构建时，Version 未变的文档会直接使用缓存
}
```

### 9.2 Redis 缓存实现示例

```go
type RedisDocCache struct {
    client *redis.Client
    prefix string
}

func (c *RedisDocCache) Get(key be_indexer.DocCacheKey) (*be_indexer.DocCacheEntry, bool) {
    // 从 Redis 获取并反序列化
}

func (c *RedisDocCache) Set(key be_indexer.DocCacheKey, entry *be_indexer.DocCacheEntry) {
    // 序列化并存储到 Redis
}

func (c *RedisDocCache) Clear() {
    // 删除所有相关 key
}
```

## 10. 后续优化方向

1. **压缩缓存数据**: 使用 protobuf 或 MessagePack 序列化
2. **缓存分片**: 支持按字段或按时间分片存储
3. **异步写入**: 缓存写入异步化，减少构建延迟
4. **缓存预热**: 支持预加载热点数据到缓存

---

**方案制定日期**: 2026-02-08  
**预计完成日期**: 2026-02-17 (9个工作日)
