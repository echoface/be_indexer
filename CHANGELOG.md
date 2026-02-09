# ChangeLog

All notable changes to this project will be documented in this file.

## [Unreleased] - 2026-02-10

### Added

#### Parser Interface Refactoring

重构 Parser 接口设计，引入 `ValueTokenizer` 和 `ValueIDGenerator` 两个核心接口，使代码更加清晰和灵活。

**主要变更：**

1. **新增 `ValueTokenizer` 接口**
   - 用于 `DefaultEntriesHolder`，将值转换为字符串列表
   - 支持双向解析：
     - `TokenizeValue`: 索引阶段，解析布尔表达式的值
     - `TokenizeAssign`: 查询阶段，解析查询参数
   - GeoHashParser、NumberParser 等已实现此接口

2. **`ValueIDGenerator` 接口增强**
   - 添加 `Name()` 方法，用于标识解析器
   - 用于 `roaringidx` 包，将值转换为 uint64 ID
   - 保持向后兼容：`FieldValueParser` 作为别名保留

3. **GeoHashParser 现在同时实现两个接口**
   - `ValueTokenizer`: 供 `DefaultEntriesHolder` 使用，生成 geohash 字符串
   - `ValueIDGenerator`: 供 `roaringidx` 使用，生成 uint64 ID
   - 核心逻辑基于字符串生成，uint64 通过字符串转换获得，符合 geohash 自然流程

**API 变更：**

```go
// 新的接口定义
type ValueTokenizer interface {
    TokenizeValue(v interface{}) ([]string, error)   // 索引阶段
    TokenizeAssign(v interface{}) ([]string, error)  // 查询阶段
}

type ValueIDGenerator interface {
    Name() string
    ParseAssign(v interface{}) ([]uint64, error)
    ParseValue(v interface{}) ([]uint64, error)
}

// 向后兼容的别名
type FieldValueParser = ValueIDGenerator  // Deprecated
```

**使用方式更新：**

```go
// DefaultEntriesHolder: 使用 RegisterFieldTokenizer
holder := be_indexer.NewDefaultEntriesHolder()
holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())
holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))

// roaringidx: 使用 ValueIDGenerator（即 FieldValueParser）
builder.ConfigureField("geo", roaringidx.FieldSetting{
    Container: roaringidx.ContainerNameDefault,
    Parser:    parser.NewGeoHashParser(nil),  // 自动适配 ValueIDGenerator
})
```

**向后兼容性：**

- `FieldValueParser` 作为 `ValueIDGenerator` 的别名保留，现有代码无需修改
- 所有原有 Parser 已实现 `Name()` 方法
- roaringidx 的使用方式保持不变

**文件改动：**
- `parser/types.go`: 新增接口定义和别名
- `parser/geohash_parser.go`: 实现双接口
- `parser/number_parser.go`: 实现 ValueTokenizer 接口
- `parser/str_parser_hash.go`: 添加 Name() 方法
- `parser/range_parser.go`: 添加 Name() 方法

---

## [Unreleased] - 2026-02-08

### Added

#### 增量索引构建 (Incremental Index Building)

新增文档级缓存机制，支持增量索引构建，大幅提升广告检索等场景下的构建性能。

**主要特性：**

1. **Document.Version 字段**
   - 新增 `Version uint64` 字段到 Document 结构体
   - 由业务层提供版本号（时间戳或序列号）
   - Version = 0 表示不使用缓存（向后兼容）

2. **DocLevelCache 接口**
   ```go
   type DocLevelCache interface {
       Get(key DocCacheKey) (*DocCacheEntry, bool)
       Set(key DocCacheKey, entry *DocCacheEntry)
       Clear()
   }
   ```
   - 业务可自定义缓存实现（内存、Redis、文件等）
   - 提供内存缓存示例实现

3. **IndexerBuilder 集成**
   - 新增 `WithDocLevelCache(cache DocLevelCache)` Builder 选项
   - 自动检测缓存命中并恢复
   - 自动保存新编译结果到缓存

4. **Schema Hash 校验**
   - 计算字段配置的哈希值
   - Schema 变化时自动清空缓存
   - 避免配置变更导致的数据不一致

**技术细节：**

- **缓存粒度**：Document 级别，每个 Document 独立缓存
- **缓存内容**：序列化后的 TxData（通过 TxData.Encode() 生成）
- **恢复机制**：通过 holder.DecodeTxData() 解码，保持接口抽象
- **类型安全**：不依赖具体 Holder 类型，支持 Default、AC、Range 等所有 Holder

**性能表现：**

在示例场景中（3 个文档，1 个更新）：
- 全量构建：237µs
- 增量构建：70µs
- **性能提升：3.3x**

对于生产环境（百万级文档，5-10%更新率），预期可实现 **5-10x 性能提升**。

**文件改动：**

- `document.go`: 添加 Version 字段
- `doc_cache.go`: 新增缓存接口和结构定义
- `index_builder.go`: 集成缓存逻辑（restoreFromCache, captureConjResult）
- `example/incremental_indexer/main.go`: 使用示例
- `doc_cache_test.go`: 单元测试

**向后兼容性：**

- Version = 0 时行为不变，完全向后兼容
- 现有代码无需修改即可正常工作
- 可选启用增量构建功能

---

## [2023-03-25]

### Added

- 支持在同一个 Conjunction 中添加同一个 field 的逻辑表达
  - eg: `{field in [1, 2, 3], not-in [2, 3, 4]} and .....`
  - input field:4 => true
  - input field:3 => false（not 有更高逻辑优先级）
  - 本库实现对逻辑 true 更严格
  - 在 roaringidx/be_indexer 两份逻辑实现中保持一致
  - 更多细节见: `./example/repeat_fields_test`

---

## [Earlier Versions]

### Features

- Boolean expression indexing 核心实现
- Roaring bitmap based indexing
- AC 自动机模式匹配容器
- 数值范围容器（支持 >, <, between 等运算符）
- 自定义 Parser 和 Holder
- 支持多值特征查询
- Compacted index 模式（性能提升约 12%）

### Limitations

- 文档 ID 最大值限制：`[-2^43, 2^43]`
- 单个 Conjunction 数量小于 256 个
- 使用前需要为每个字段完成配置

## Migration Guide

### From Older Versions to v2026.02.08

1. **无需改动**：现有代码完全向后兼容
2. **启用增量构建**（可选）：
   ```go
   // 添加缓存
   cache := NewMemoryDocCache()
   builder := be_indexer.NewIndexerBuilder(
       be_indexer.WithDocLevelCache(cache),
   )
   
   // 设置 Version
   doc := be_indexer.NewDocument(id)
   doc.Version = uint64(updateTime.Unix())
   ```

## Contributing

欢迎提交 Issue 和 PR！

## License

MIT License - see [LICENSE](LICENSE) file for details.