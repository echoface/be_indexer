# Be Indexer - 布尔表达式索引库文档

## 项目概述

be_indexer 是一个基于布尔表达式索引的高性能库，源自论文[Boolean expression indexing](https://theory.stanford.edu/~sergei/papers/vldb09-indexing.pdf)。该库主要用于解决广告投放、商品检索、内容推荐等场景下的规则匹配问题。

### 核心特性

- ✅ 支持两种索引实现（默认实现 & 紧凑型实现）
- ✅ 支持基于Roaring Bitmap的高性能实现（roaringidx）
- ✅ 支持多种数据类型：字符串、数值、范围查询
- ✅ 支持Aho-Corasick模式匹配
- ✅ 支持地理哈希解析
- ✅ 支持自定义容器和解析器
- ✅ 提供灵活的缓存机制

### 限制

- 文档ID范围：[-2^43, 2^43]
- 单个文档的Conjunction数量：< 256

---

## 文档导航

### 📚 核心文档

| 文档 | 描述 | 推荐阅读人群 |
|------|------|-------------|
| [API参考手册](./API_REFERENCE.md) | 完整的API文档，包含所有接口和函数说明 | 所有用户 |
| [快速入门指南](./QUICK_START.md) | 从零开始学习，包含基础概念和示例 | 新手用户 |
| [架构设计文档](./ARCHITECTURE.md) | 深入理解内部实现和算法原理 | 高级用户 |
| [示例集合](./EXAMPLES.md) | 各种场景的完整示例代码 | 所有用户 |

### 🎯 应用场景

1. **广告投放系统**
   - 基于用户特征匹配广告规则
   - 多维度用户画像匹配
   - 参考: [广告投放示例](../example/roaringidx_usage/example_usage.go)

2. **电商筛选系统**
   - 商品属性组合查询
   - 价格范围筛选
   - 多条件商品推荐

3. **内容推荐系统**
   - 基于标签的内容匹配
   - 用户兴趣推荐
   - 视频/文章推荐

4. **规则引擎**
   - 复杂业务规则匹配
   - 权限控制
   - 风险评估

5. **地理信息系统**
   - 基于位置的信息检索
   - 附近商店推荐

---

## 快速开始

### 安装

```bash
go get github.com/echoface/be_indexer
```

### 最小示例

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    // 1. 创建构建器
    builder := be_indexer.NewIndexerBuilder()

    // 2. 构建文档
    doc := be_indexer.NewDocument(1)
    doc.AddConjunction(
        be_indexer.NewConjunction().
            Include("age", be_indexer.NewIntValues(18, 25)).
            Include("city", be_indexer.NewStrValues("beijing")),
    )

    // 3. 添加文档并构建索引
    builder.AddDocument(doc)
    indexer := builder.BuildIndex()

    // 4. 检索
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "age":  be_indexer.NewIntValues(20),
        "city": be_indexer.NewStrValues("beijing"),
    }

    result, _ := indexer.Retrieve(assigns)
    fmt.Println("匹配文档:", result)
}
```

---

## 选择合适的实现

### 对比表

| 特性 | 默认索引器 | 紧凑型索引器 | Roaringidx |
|------|------------|--------------|------------|
| 内存使用 | 标准 | 节省 | 最低 |
| 性能 | 基准 | +12% | 最高 |
| 实现复杂度 | 简单 | 简单 | 中等 |
| 适用场景 | 一般应用 | 内存敏感 | 大规模数据 |
| 文档ID范围 | [-2^43, 2^43] | [-2^43, 2^43] | [-2^56, 2^56] |

### 推荐选择

```go
// 小规模数据 (< 10万文档)
builder := be_indexer.NewIndexerBuilder()

// 内存敏感场景
builder := be_indexer.NewCompactIndexerBuilder()

// 大规模数据 (> 100万文档)
builder := roaringidx.NewIndexerBuilder()
```

---

## 核心概念

### Document（文档）
表示一个可索引的数据实体，每个Document包含多个Conjunction。

```go
doc := be_indexer.NewDocument(1)
doc.AddConjunction(conj1, conj2)
```

### Conjunction（连接）
表示一个AND表达式组，包含多个字段的匹配条件。

```go
conj := be_indexer.NewConjunction()
conj.Include("age", be_indexer.NewIntValues(18, 25))
conj.Exclude("city", be_indexer.NewStrValues("rural"))
```

### Assignments（查询分配）
检索时的条件，字段到值的映射。

```go
assigns := map[be_indexer.BEField]be_indexer.Values{
    "age": be_indexer.NewIntValues(20),
}
```

### Indexer（索引器）
构建和检索索引的核心组件。

```go
indexer := builder.BuildIndex()
result, _ := indexer.Retrieve(assigns)
```

---

## 常用配置

### 1. 字段配置

```go
// 默认容器
builder.ConfigField("category", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,
})

// AC自动机（字符串模式匹配）
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})

// 扩展范围（范围查询优化）
builder.ConfigField("score", be_indexer.FieldOption{
    Container: HolderNameExtendRange,
})
```

### 2. 错误处理

```go
// 跳过错误的Conjunction（推荐用于大数据）
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
)

// 返回错误
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.ErrorBadConj),
)
```

### 3. 缓存配置

```go
type MyCache struct {
    data map[ConjID][]byte
}

func (c *MyCache) Reset() { ... }
func (c *MyCache) Get(conjID ConjID) ([]byte, bool) { ... }
func (c *MyCache) Set(conjID ConjID, data []byte) { ... }

builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithCacheProvider(&MyCache{}),
)
```

---

## 性能优化建议

### 1. 索引构建优化

- ✅ 使用紧凑型构建器提升12%性能
- ✅ 预先配置所有字段
- ✅ 使用缓存机制
- ✅ 批量添加文档

### 2. 检索优化

- ✅ 避免过于严格的查询条件
- ✅ 适当使用Include而不是Exclude
- ✅ 考虑使用roaringidx处理大规模数据

### 3. 内存优化

- ✅ 选择合适的容器类型
- ✅ 使用紧凑型索引器
- ✅ 及时释放资源

---

## 示例代码

### 基础示例

参考：[QUICK_START.md](./QUICK_START.md#第一个示例)

### 高级示例

参考：[EXAMPLES.md](./EXAMPLES.md)

### 完整应用示例

- [be_indexer使用示例](../example/be_indexer_usage/main.go)
- [roaringidx使用示例](../example/roaringidx_usage/example_usage.go)
- [紧凑型索引器示例](../example/compacted_indexer/compacted_index.go)
- [AC自动机示例](../example/ac_cedar_test/main.go)
- [geohash示例](../example/geohash_exmaple/geohash_example.go)

---

## 常见问题

### Q: 索引构建失败怎么办？
A: 检查值类型是否正确，使用`SkipBadConj`跳过错误数据

### Q: 检索结果为空？
A: 检查Assignments中的值是否与Document中定义一致

### Q: 如何提高性能？
A: 使用紧凑型构建器、预先配置字段、使用缓存

### Q: 选择哪个实现？
A: 小规模用默认/紧凑型，大规模用roaringidx

---

## 最佳实践

1. **预先配置字段**
   ```go
   for field := range allFields {
       builder.ConfigField(field, be_indexer.FieldOption{...})
   }
   ```

2. **使用错误处理策略**
   ```go
   builder := be_indexer.NewIndexerBuilder(
       be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
   )
   ```

3. **为重复模式使用缓存**
   ```go
   builder := be_indexer.NewIndexerBuilder(
       be_indexer.WithCacheProvider(myCache),
   )
   ```

4. **选择合适的容器**
   ```go
   // 字符串模式匹配
   builder.ConfigField("keyword", be_indexer.FieldOption{
       Container: be_indexer.HolderNameACMatcher,
   })
   ```

---

## 贡献指南

欢迎提交Issue和Pull Request！

### 开发环境设置

```bash
git clone https://github.com/echoface/be_indexer.git
cd be_indexer
go test ./...
```

---

## 许可证

MIT License - 详见 [LICENSE](../LICENSE)

---

## 联系方式

- 作者: [gonghuan.dev](mailto:gonghuan.dev@gmail.com)
- 项目地址: [https://github.com/echoface/be_indexer](https://github.com/echoface/be_indexer)

---

## 更新日志

### v1.0.0
- 支持布尔表达式索引
- 支持AC自动机模式匹配
- 支持roaringidx实现
- 支持地理哈希解析

### v1.1.0
- 添加紧凑型索引器
- 性能提升12%
- 优化内存使用

### v1.2.0
- 支持多表达式同一字段
- 增强错误处理
- 添加更多示例

---

**开始使用吧！** 🚀

建议阅读顺序：
1. [快速入门指南](./QUICK_START.md) - 了解基础概念
2. [API参考手册](./API_REFERENCE.md) - 掌握所有API
3. [示例集合](./EXAMPLES.md) - 学习实际应用
4. [架构设计文档](./ARCHITECTURE.md) - 深入理解原理
