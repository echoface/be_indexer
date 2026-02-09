# how to customize parser/tokenizer

## 概述

parser/tokenizer 的选择取决于索引数据的容器类型：

- **DefaultEntriesHolder**: 使用 `ValueTokenizer` 接口，将值转换为字符串列表
- **roaringidx**: 使用 `ValueIDGenerator` 接口，将值转换为 uint64 ID

例如，模式匹配容器 `ac_matcher` 需要字符串输入：

```
doc 1:  keywords in ["红包", "av", "adult"]

indexing query input: "发红包来领取adult av观看福利"
indexing query output: [doc1]
```

## 接口定义

### ValueTokenizer (DefaultEntriesHolder 使用)

```go
type ValueTokenizer interface {
    // TokenizeValue 索引阶段：解析布尔表达式的值
    TokenizeValue(v interface{}) ([]string, error)

    // TokenizeAssign 查询阶段：解析查询参数
    TokenizeAssign(v interface{}) ([]string, error)
}
```

### ValueIDGenerator (roaringidx 使用)

```go
type ValueIDGenerator interface {
    Name() string
    ParseAssign(v interface{}) ([]uint64, error)
    ParseValue(v interface{}) ([]uint64, error)
}

// 向后兼容别名
type FieldValueParser = ValueIDGenerator
```

## 为 DefaultEntriesHolder 配置自定义 Parser

DefaultEntriesHolder 通过 `RegisterFieldTokenizer` 方法注册字段级 parser：

```go
func init() {
    be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
        holder := be_indexer.NewDefaultEntriesHolder()
        
        // 为 age 字段注册范围解析器
        holder.RegisterFieldTokenizer("age", parser.NewNumRangeParser())
        
        // 为 geo 字段注册 geohash 解析器
        holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))
        
        return holder
    })
}
```

### 双接口实现的 Parser

以下 Parser 同时实现了两个接口，可以在两种容器中使用：

```go
// GeoHashParser: 同时实现 ValueTokenizer 和 ValueIDGenerator
geohashParser := parser.NewGeoHashParser(nil)

// 用于 DefaultEntriesHolder (ValueTokenizer 接口)
holder.RegisterFieldTokenizer("geo", geohashParser)

// 用于 roaringidx (ValueIDGenerator 接口)
builder.ConfigureField("geo", roaringidx.FieldSetting{
    Container: roaringidx.ContainerNameDefault,
    Parser:    geohashParser,
})
```

## 内置 Parser

### 1. NumberParser

```go
// 用于 DefaultEntriesHolder
holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())

// 用于 roaringidx
builder.ConfigureField("tag", roaringidx.FieldSetting{
    Parser: parser.NewNumberParser(),
})
```

### 2. GeoHashParser

```go
option := &parser.GeoOption{
    Precision:               6,  // geohash 精度
    CompressPrecisionMin:    4,  // 最小压缩精度
    CompressPrecisionCutoff: 6,  // 压缩截断精度
}
parser := parser.NewGeoHashParser(option)
```

### 3. NumberRangeParser

```go
// 支持范围表达式: "start:end:step"
// 如 "18:30:1" 表示 18 到 30 的所有整数
holder.RegisterFieldTokenizer("age", parser.NewNumRangeParser())
```

### 4. StrHashParser

```go
// 字符串哈希解析器
builder.ConfigureField("package", roaringidx.FieldSetting{
    Parser: parser.NewStrHashParser(),
})
```

## 自定义 Parser 实现

实现 `ValueTokenizer` 接口供 DefaultEntriesHolder 使用：

```go
type MyTokenizer struct{}

func (t *MyTokenizer) TokenizeValue(v interface{}) ([]string, error) {
    // 索引阶段：将值转换为字符串列表
    // 如：将 "31:121:1000" 展开为多个 geohash
}

func (t *MyTokenizer) TokenizeAssign(v interface{}) ([]string, error) {
    // 查询阶段：将查询参数转换为字符串
    // 如：将 [31.2, 121.5] 转换为 geohash
}

// 注册使用
holder.RegisterFieldTokenizer("myfield", &MyTokenizer{})
```

实现 `ValueIDGenerator` 接口供 roaringidx 使用：

```go
type MyIDGenerator struct{}

func (g *MyIDGenerator) Name() string {
    return "my_generator"
}

func (g *MyIDGenerator) ParseValue(v interface{}) ([]uint64, error) {
    // 索引阶段：将值转换为 uint64 ID 列表
}

func (g *MyIDGenerator) ParseAssign(v interface{}) ([]uint64, error) {
    // 查询阶段：将查询参数转换为 uint64 ID
}

// 配置使用
builder.ConfigureField("myfield", roaringidx.FieldSetting{
    Parser: &MyIDGenerator{},
})
```

实现双接口（推荐）：

```go
type DualParser struct{}

// ValueTokenizer 接口
func (p *DualParser) TokenizeValue(v interface{}) ([]string, error) { ... }
func (p *DualParser) TokenizeAssign(v interface{}) ([]string, error) { ... }

// ValueIDGenerator 接口
func (p *DualParser) Name() string { return "dual" }
func (p *DualParser) ParseValue(v interface{}) ([]uint64, error) {
    // 可以先调用 TokenizeValue，再将字符串转为 uint64
    strs, err := p.TokenizeValue(v)
    if err != nil {
        return nil, err
    }
    return convertStringsToUint64(strs), nil
}
func (p *DualParser) ParseAssign(v interface{}) ([]uint64, error) { ... }
```

## 完整示例

```go
package main

import (
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/parser"
)

func main() {
    // 注册自定义 holder，配置 geohash parser
    be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
        holder := be_indexer.NewDefaultEntriesHolder()
        holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))
        holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())
        return holder
    })

    // 创建 builder
    builder := be_indexer.NewIndexerBuilder()

    // 添加文档
    doc := be_indexer.NewDocument(1)
    doc.AddConjunction(be_indexer.NewConjunction().
        In("geo", "31.21275902:121.53779984:1000").
        In("tag", []int64{1, 2, 3}))

    builder.AddDocument(doc)
    indexer := builder.BuildIndex()

    // 检索
    result, err := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
        "geo": [2]float64{31.21275902, 121.53779984},
        "tag": 1,
    })
}
```
