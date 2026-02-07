# 快速入门指南

本指南将帮助您快速上手be_indexer库，实现布尔表达式索引功能。

## 目录

1. [安装](#安装)
2. [基本概念](#基本概念)
3. [第一个示例](#第一个示例)
4. [常用场景](#常用场景)
5. [性能优化建议](#性能优化建议)
6. [故障排除](#故障排除)

---

## 安装

确保您的Go版本 >= 1.16

```bash
go get github.com/echoface/be_indexer
```

---

## 基本概念

### 核心组件

1. **Document**: 表示一个可索引的文档
2. **Conjunction**: 表示一个AND表达式组
3. **Assignments**: 检索时的查询条件
4. **Indexer**: 构建和检索索引的核心组件

### 数据流程

```
构建阶段：
Document -> Conjunction -> IndexerBuilder -> BEIndex

检索阶段：
Assignments -> BEIndex -> DocIDList
```

---

## 第一个示例

让我们创建一个简单的商品推荐系统示例：

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    // 1. 创建索引构建器
    builder := be_indexer.NewIndexerBuilder(
        be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
    )

    // 2. 构建商品数据（每个商品是一个Document）
    products := []*be_indexer.Document{
        // 商品1: 年龄18-25岁，城市在北京的VIP用户
        be_indexer.NewDocument(1).AddConjunction(
            be_indexer.NewConjunction().
                Include("age", be_indexer.NewIntValues(18, 25)).
                Include("city", be_indexer.NewStrValues("beijing")).
                Include("is_vip", be_indexer.NewStrValues("true")),
        ),
        // 商品2: 年龄大于30岁，不在农村的用户
        be_indexer.NewDocument(2).AddConjunction(
            be_indexer.NewConjunction().
                GreaterThan("age", 30).
                Exclude("city", be_indexer.NewStrValues("rural")),
        ),
        // 商品3: 年龄25岁，城市在上海或广州
        be_indexer.NewDocument(3).AddConjunction(
            be_indexer.NewConjunction().
                Include("age", be_indexer.NewIntValues(25)).
                Include("city", be_indexer.NewStrValues("shanghai", "guangzhou")),
        ),
    }

    // 3. 将商品添加到索引
    err := builder.AddDocument(products...)
    if err != nil {
        panic(err)
    }

    // 4. 构建索引
    indexer := builder.BuildIndex()

    // 5. 检索：寻找匹配的商品
    // 场景1: 用户年龄25岁，在北京，VIP
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "age":     be_indexer.NewIntValues(25),
        "city":    be_indexer.NewStrValues("beijing"),
        "is_vip":  be_indexer.NewStrValues("true"),
    }

    result, err := indexer.Retrieve(assigns)
    if err != nil {
        panic(err)
    }

    fmt.Println("场景1匹配的商品ID:", result)
    // 输出: 场景1匹配的商品ID: [1]

    // 场景2: 用户年龄20岁，在上海，非VIP
    assigns2 := map[be_indexer.BEField]be_indexer.Values{
        "age":    be_indexer.NewIntValues(20),
        "city":   be_indexer.NewStrValues("shanghai"),
        "is_vip": be_indexer.NewStrValues("false"),
    }

    result2, err := indexer.Retrieve(assigns2)
    if err != nil {
        panic(err)
    }

    fmt.Println("场景2匹配的商品ID:", result2)
    // 输出: 场景2匹配的商品ID: [3]
}
```

**运行结果：**
```
场景1匹配的商品ID: [1]
场景2匹配的商品ID: [3]
```

---

## 常用场景

### 场景1: 广告投放系统

基于用户特征匹配广告投放规则。

```go
// 构建广告规则
adRules := []*be_indexer.Document{
    // 规则1: 面向北京、上海的18-25岁VIP用户
    be_indexer.NewDocument(1001).AddConjunction(
        be_indexer.NewConjunction().
            Include("age", be_indexer.NewIntValues(18, 25)).
            Include("city", be_indexer.NewStrValues("beijing", "shanghai")).
            Include("is_vip", be_indexer.NewStrValues("true")),
    ),
    // 规则2: 面向非VIP且不在农村的用户
    be_indexer.NewDocument(1002).AddConjunction(
        be_indexer.NewConjunction().
            Include("is_vip", be_indexer.NewStrValues("false")).
            Exclude("city", be_indexer.NewStrValues("rural")),
    ),
}

// 用户画像
userProfile := map[be_indexer.BEField]be_indexer.Values{
    "age":     be_indexer.NewIntValues(22),
    "city":    be_indexer.NewStrValues("beijing"),
    "is_vip":  be_indexer.NewStrValues("true"),
}

matchedAds, _ := indexer.Retrieve(userProfile)
fmt.Println("匹配的广告:", matchedAds)
```

### 场景2: 电商筛选系统

基于商品属性进行筛选。

```go
// 构建商品索引
products := []*be_indexer.Document{
    be_indexer.NewDocument(2001).AddConjunction(
        be_indexer.NewConjunction().
            GreaterThan("price", 100).
            LessThan("price", 500).
            Include("category", be_indexer.NewStrValues("electronics")),
    ),
    be_indexer.NewDocument(2002).AddConjunction(
        be_indexer.NewConjunction().
            Include("category", be_indexer.NewStrValues("books")).
            LessThan("price", 50),
    ),
}

// 筛选条件
filter := map[be_indexer.BEField]be_indexer.Values{
    "price":    be_indexer.NewIntValues(200),  // 价格为200
    "category": be_indexer.NewStrValues("electronics"),
}

matchedProducts, _ := indexer.Retrieve(filter)
```

### 场景3: 内容推荐系统

基于标签匹配内容。

```go
// 构建内容索引
contents := []*be_indexer.Document{
    be_indexer.NewDocument(3001).AddConjunction(
        be_indexer.NewConjunction().
            Include("tags", be_indexer.NewStrValues("tech", "ai", "machine-learning")),
    ),
    be_indexer.NewDocument(3002).AddConjunction(
        be_indexer.NewConjunction().
            Include("tags", be_indexer.NewStrValues("art", "design", "creative")),
    ),
}

// 用户兴趣标签
userTags := map[be_indexer.BEField]be_indexer.Values{
    "tags": be_indexer.NewStrValues("ai", "tech"),
}

recommendedContent, _ := indexer.Retrieve(userTags)
```

### 场景4: 多值字段查询

对于同一个字段有多个条件的情况（如重复字段测试）。

```go
// 在同一个Conjunction中为同一字段添加多个条件
doc := be_indexer.NewDocument(4001)
conj := be_indexer.NewConjunction()

// 同一字段的多个条件
conj.Include("category", be_indexer.NewStrValues("tech"))
conj.Exclude("category", be_indexer.NewStrValues("legacy"))

doc.AddConjunction(conj)

// 检索
query := map[be_indexer.BEField]be_indexer.Values{
    "category": be_indexer.NewStrValues("tech", "legacy"),
}

result, _ := indexer.Retrieve(query)
```

### 场景5: 数值范围查询

```go
// 使用范围查询
doc := be_indexer.NewDocument(5001)
doc.AddConjunction(
    be_indexer.NewConjunction().
        GreaterThan("score", 80).    // score > 80
        LessThan("age", 60).       // age < 60
        Between("price", 100, 1000), // price between [100, 1000]
)

// 检索
query := map[be_indexer.BEField]be_indexer.Values{
    "score": be_indexer.NewIntValues(90),
    "age":   be_indexer.NewIntValues(30),
    "price": be_indexer.NewIntValues(500),
}

result, _ := indexer.Retrieve(query)
```

---

## 性能优化建议

### 1. 选择合适的索引类型

```go
// 标准索引器
builder := be_indexer.NewIndexerBuilder()

// 紧凑型索引器（性能提升12%）
builder := be_indexer.NewCompactIndexerBuilder()
```

### 2. 预先配置字段

```go
// 好的做法：预先配置所有字段
builder.ConfigField("age", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,
})
builder.ConfigField("city", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,
})

// 避免：运行时动态创建字段（性能较差）
```

### 3. 使用合适的容器

```go
// 字符串模式匹配（如关键词匹配）
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})

// 范围查询优化
builder.ConfigField("score", be_indexer.FieldOption{
    Container: HolderNameExtendRange,
})

// 普通场景
builder.ConfigField("category", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,
})
```

### 4. 使用缓存

```go
type SimpleCache struct {
    data map[ConjID][]byte
}

func (c *SimpleCache) Reset() {
    c.data = make(map[ConjID][]byte)
}

func (c *SimpleCache) Get(conjID ConjID) ([]byte, bool) {
    data, ok := c.data[conjID]
    return data, ok
}

func (c *SimpleCache) Set(conjID ConjID, data []byte) {
    c.data[conjID] = data
}

builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithCacheProvider(&SimpleCache{data: make(map[ConjID][]byte)}),
)
```

### 5. 错误处理策略

```go
// 对于大数据量，使用SkipBadConj避免中断
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
)
```

---

## 故障排除

### 问题1: 索引构建失败

**现象：**
```
panic: indexing conj:<1,0,2> fail: parse field:age value fail
```

**原因：** 值解析失败，可能是数据类型不匹配

**解决方案：**
```go
// 确保值类型正确
Include("age", be_indexer.NewIntValues(25))  // 正确
// Include("age", "25")  // 错误：字符串类型
```

### 问题2: 检索结果为空

**原因：** 查询条件过于严格或字段值不匹配

**解决方案：**
1. 检查Assignments中的值是否与Document中定义的值一致
2. 检查是否使用了正确的操作符（Include vs Exclude）

```go
// 打印调试信息
result, err := indexer.Retrieve(assigns,
    be_indexer.WithStepDetail(),
    be_indexer.WithDumpEntries())
```

### 问题3: ID溢出错误

**现象：**
```
panic: id overflow, id:9000000000000, idx:0 size:2
```

**原因：** DocID超过限制范围 [-2^43, 2^43]

**解决方案：**
```go
// 检查DocID是否在有效范围内
if !be_indexer.ValidDocID(docID) {
    log.Fatal("DocID超出范围")
}
```

### 问题4: 内存使用过高

**原因：** 文档数量过多或字段配置不当

**解决方案：**
1. 使用roaringidx替代默认索引器
2. 减少不必要的字段
3. 使用紧凑型索引器
4. 配置合适的容器类型

### 问题5: 性能下降

**解决方案：**
1. 使用紧凑型构建器
2. 预先配置所有字段
3. 使用缓存
4. 选择合适的容器
5. 避免运行时字段配置

---

## 总结

be_indexer是一个非常强大的布尔表达式索引库，适用于各种规则匹配场景。通过合理配置和使用，可以实现高性能的检索系统。

记住：
- 预先配置字段以获得最佳性能
- 选择合适的容器类型
- 对于大规模数据，考虑使用roaringidx
- 使用SkipBadConj处理大量数据
- 定期分析和优化索引配置

更多详细信息，请参考[API参考文档](./API_REFERENCE.md)。
