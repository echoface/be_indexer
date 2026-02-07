# Boolean Expression Indexer - API Reference

## 目录

1. [概述](#概述)
2. [核心数据类型](#核心数据类型)
3. [文档和表达式API](#文档和表达式api)
4. [索引构建器API](#索引构建器api)
5. [索引检索API](#索引检索api)
6. [解析器API](#解析器api)
7. [容器类型](#容器类型)
8. [Roaringidx API](#roaringidx-api)
9. [实用工具API](#实用工具api)
10. [错误处理](#错误处理)
11. [性能优化](#性能优化)

---

## 概述

be_indexer 是一个基于布尔表达式索引的库，源自论文[Boolean expression indexing](https://theory.stanford.edu/~sergei/papers/vldb09-indexing.pdf)。它主要用于解决广告、商品检索等场景下的规则匹配问题。

### 核心特性

- 支持两种索引实现：默认实现和紧凑型实现
- 支持基于Roaring Bitmap的高性能实现（roaringidx）
- 支持多种数据类型：字符串、数值、范围查询
- 支持Aho-Corasick模式匹配
- 支持地理哈希解析

### 限制

- 文档ID范围：[-2^43, 2^43]
- 单个文档的Conjunction数量：< 256
- 字段需要预先配置（建议）

---

## 核心数据类型

### DocID

文档标识符类型。

```go
type DocID int64

const (
    MaxDocID = 0x7FFFFFFFFFF  // 最大文档ID
)

// 检查DocID是否有效
func ValidDocID(id DocID) bool
```

**示例：**
```go
docID := be_indexer.DocID(100)
if be_indexer.ValidDocID(docID) {
    // 有效的文档ID
}
```

### BEField

字段名称类型。

```go
type BEField string
```

**示例：**
```go
field := be_indexer.BEField("age")
```

### Values

值类型接口，可以是以下类型之一：
- `[]int`, `[]int32`, `[]int64`
- `[]string`
- `[]uint`, `[]uint8`, `[]uint16`, `[]uint32`
- `[]float32`, `[]float64`
- `[]interface{}`
- 单个数值或字符串

```go
type Values interface{}
```

**辅助函数：**
```go
// 创建整数值
func NewIntValues(v int, o ...int) Values
func NewInt32Values(v int32, o ...int32) Values
func NewInt64Values(v int64, o ...int64) Values

// 创建字符串值
func NewStrValues(v string, ss ...string) Values
```

**示例：**
```go
ages := be_indexer.NewIntValues(18, 25, 30)
cities := be_indexer.NewStrValues("beijing", "shanghai")
```

### BoolValues

布尔值表达式，描述一个字段的匹配条件。

```go
type BoolValues struct {
    Incl     bool     `json:"inc"`                // true: 包含, false: 排除
    Value    Values   `json:"value"`              // 值列表
    Operator ValueOpt `json:"operator,omitempty"` // 操作符类型
}
```

### ValueOpt

值操作符类型。

```go
type ValueOpt int

const (
    ValueOptEQ      ValueOpt = 0  // 等于
    ValueOptGT      ValueOpt = 1  // 大于
    ValueOptLT      ValueOpt = 2  // 小于
    ValueOptBetween ValueOpt = 3  // 介于两者之间
)
```

### BooleanExpr

布尔表达式。

```go
type BooleanExpr struct {
    BoolValues
    Field BEField `json:"field"`
}

// 创建布尔表达式
func NewBoolExpr(field BEField, inc bool, v Values) *BooleanExpr
func NewBoolExpr2(field BEField, expr BoolValues) *BooleanExpr
```

### Assignments

查询分配，字段到值的映射。

```go
type Assignments map[BEField]Values

// 获取分配大小
func (ass Assignments) Size() (size int)
```

**示例：**
```go
assigns := map[be_indexer.BEField]be_indexer.Values{
    "age":  be_indexer.NewIntValues(25),
    "city": be_indexer.NewStrValues("beijing", "shanghai"),
    "tag":  be_indexer.NewStrValues("vip", "premium"),
}
```

---

## 文档和表达式API

### Document

文档结构，表示一个可索引的文档。

```go
type Document struct {
    ID   DocID          `json:"id"`   // 文档ID
    Cons []*Conjunction `json:"cons"` // 布尔表达式列表（OR关系）
}

// 创建新文档
func NewDocument(id DocID) *Document

// 添加Conjunction
func (doc *Document) AddConjunction(cons ...*Conjunction) *Document
func (doc *Document) AddConjunctions(conj *Conjunction, others ...*Conjunction) *Document

// JSON序列化
func (doc *Document) JSONString() string

// 字符串表示
func (doc *Document) String() string
```

**示例：**
```go
doc := be_indexer.NewDocument(1)
doc.AddConjunction(
    be_indexer.NewConjunction().
        Include("age", be_indexer.NewIntValues(18, 25)).
        Include("city", be_indexer.NewStrValues("beijing")),
    be_indexer.NewConjunction().
        Include("vip", be_indexer.NewStrValues("true")),
)
```

### Conjunction

Conjunction表示一个AND表达式组。

```go
type Conjunction struct {
    Expressions map[BEField][]*BoolValues `json:"exprs"`
}

// 创建新Conjunction
func NewConjunction() *Conjunction

// 添加包含条件
func (conj *Conjunction) In(field BEField, values Values) *Conjunction
func (conj *Conjunction) Include(field BEField, values Values) *Conjunction

// 添加排除条件
func (conj *Conjunction) NotIn(field BEField, values Values) *Conjunction
func (conj *Conjunction) Exclude(field BEField, values Values) *Conjunction

// 添加范围条件
func (conj *Conjunction) GreaterThan(field BEField, value int64) *Conjunction
func (conj *Conjunction) LessThan(field BEField, value int64) *Conjunction
func (conj *Conjunction) Between(field BEField, l, h int64) *Conjunction

// 添加布尔表达式
func (conj *Conjunction) AddBoolExprs(exprs ...*BooleanExpr) *Conjunction

// 添加表达式（兼容性函数）
func (conj *Conjunction) AddExpression3(field string, include bool, values Values) *Conjunction

// 统计信息
func (conj *Conjunction) CalcConjSize() (size int)
func (conj *Conjunction) ExpressionCount() (size int)

// 字符串表示
func (conj *Conjunction) String() string

// JSON序列化
func (conj *Conjunction) JSONString() string
```

**示例：**
```go
// 使用Include/Exclude
conj := be_indexer.NewConjunction().
    Include("age", be_indexer.NewIntValues(18, 25, 30)).
    Exclude("city", be_indexer.NewStrValues("rural"))

// 使用范围查询
conj2 := be_indexer.NewConjunction().
    GreaterThan("score", 80).
    LessThan("age", 60).
    Between("price", 100, 1000)

// 直接使用BooleanExpr
expr := be_indexer.NewBoolExpr("vip", true, be_indexer.NewStrValues("gold"))
conj3 := be_indexer.NewConjunction().
    AddBoolExprs(expr)
```

### 创建BoolValues的辅助函数

```go
// 创建大于条件的BoolValues
func NewGTBoolValue(value int64) BoolValues

// 创建小于条件的BoolValues
func NewLTBoolValue(value int64) BoolValues

// 创建自定义BoolValues
func NewBoolValue(op ValueOpt, value Values, incl bool) BoolValues
```

**示例：**
```go
// 创建大于100的包含条件
bv := be_indexer.NewGTBoolValue(100)

// 创建介于50-200的包含条件
bv2 := be_indexer.NewBoolValue(be_indexer.ValueOptBetween, []int64{50, 200}, true)
```

---

## 索引构建器API

### IndexerBuilder

索引构建器，用于构建布尔表达式索引。

```go
type IndexerBuilder struct {
    // 私有字段...
}

// 创建标准索引构建器
func NewIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder

// 创建紧凑型索引构建器（性能提升约12%）
func NewCompactIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder
```

### BuilderOption

构建器选项配置函数。

```go
type BuilderOpt func(builder *IndexerBuilder)

// 设置错误行为
func WithBadConjBehavior(v BadConjBehavior) BuilderOpt

// 设置缓存提供者
func WithCacheProvider(provider CacheProvider) BuilderOpt

// 设置索引器类型
func WithIndexerType(t IndexerType) BuilderOpt
```

### BadConjBehavior

错误行为类型。

```go
type BadConjBehavior int

const (
    ErrorBadConj = 0  // 返回错误（默认）
    SkipBadConj  = 1  // 跳过错误的Conjunction
    PanicBadConj = 2  // 触发panic
)

type IndexerType int

const (
    IndexerTypeDefault = IndexerType(0)  // 默认类型
    IndexerTypeCompact = IndexerType(1)  // 紧凑型
)
```

### CacheProvider

缓存提供者接口。

```go
type CacheProvider interface {
    Reset()
    Get(conjID ConjID) ([]byte, bool)
    Set(conjID ConjID, data []byte)
}
```

### IndexerBuilder 方法

```go
// 重置构建器
func (b *IndexerBuilder) Reset()

// 配置字段
func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption)

// 添加文档
func (b *IndexerBuilder) AddDocument(docs ...*Document) error

// 构建索引
func (b *IndexerBuilder) BuildIndex() BEIndex
```

### FieldOption

字段配置选项。

```go
type FieldOption struct {
    Container string  // 指定容器类型
}

const (
    HolderNameDefault     = "default"
    HolderNameACMatcher   = "ac_matcher"
    HolderNameExtendRange = "ext_range"
)
```

**完整示例：**
```go
// 创建构建器
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
    be_indexer.WithCacheProvider(myCache),
)

// 或者使用紧凑型构建器
// builder := be_indexer.NewCompactIndexerBuilder()

// 配置字段
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})
builder.ConfigField("age", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,
})

// 添加文档
docs := []*be_indexer.Document{
    be_indexer.NewDocument(1).AddConjunction(
        be_indexer.NewConjunction().
            Include("age", be_indexer.NewIntValues(18, 25)).
            Include("city", be_indexer.NewStrValues("beijing")),
    ),
    be_indexer.NewDocument(2).AddConjunction(
        be_indexer.NewConjunction().
            GreaterThan("age", 30).
            Include("vip", be_indexer.NewStrValues("true")),
    ),
}
err := builder.AddDocument(docs...)
if err != nil {
    log.Fatal(err)
}

// 构建索引
indexer := builder.BuildIndex()
```

---

## 索引检索API

### BEIndex

索引接口。

```go
type BEIndex interface {
    Retrieve(queries Assignments, opt ...IndexOpt) (DocIDList, error)
    RetrieveWithCollector(Assignments, ResultCollector, ...IndexOpt) error
    DumpEntries(sb *strings.Builder)
    DumpIndexInfo(sb *strings.Builder)
}
```

### IndexOpt

检索选项配置函数。

```go
type IndexOpt func(ctx *retrieveContext)

// 启用步骤详细信息
func WithStepDetail() IndexOpt

// 启用条目详细信息
func WithDumpEntries() IndexOpt

// 指定自定义结果收集器
func WithCollector(fn ResultCollector) IndexOpt
```

### ResultCollector

结果收集器接口（可自定义）。

```go
type ResultCollector interface {
    Collect(docID DocID)
    Result() DocIDList
    Reset()
}
```

### Indexer 检索方法

```go
// 检索匹配文档
result, err := indexer.Retrieve(assigns)

// 检索并获取详细信息
result, err := indexer.retrieve(assigns,
    be_indexer.WithStepDetail(),
    be_indexer.WithDumpEntries())

// 使用自定义收集器
collector := be_indexer.NewDocIDCollector()
err := indexer.RetrieveWithCollector(assigns, collector)
result := collector.Result()
```

**完整检索示例：**
```go
// 构建索引
indexer := builder.BuildIndex()

// 准备查询
assigns := map[be_indexer.BEField]be_indexer.Values{
    "age":  be_indexer.NewIntValues(20, 25, 30),
    "city": be_indexer.NewStrValues("beijing", "shanghai"),
    "tag":  be_indexer.NewStrValues("vip"),
}

// 执行检索
result, err := indexer.Retrieve(assigns)
if err != nil {
    log.Fatal(err)
}

fmt.Println("匹配的文档:", result)

// 检索并获取详细信息
result, err = indexer.Retrieve(assigns,
    be_indexer.WithStepDetail(),
    be_indexer.WithDumpEntries())
```

### DocIDList

文档ID列表。

```go
type DocIDList []DocID

// 检查是否包含某个ID
func (s DocIDList) Contain(id DocID) bool

// 差集运算
func (s DocIDList) Sub(other DocIDList) (r DocIDList)

// 实现sort.Interface
func (s DocIDList) Len() int
func (s DocIDList) Swap(i, j int)
func (s DocIDList) Less(i, j int) bool
```

---

## 解析器API

### FieldValueParser

字段值解析器接口。

```go
type FieldValueParser interface {
    Name() string
    ParseAssign(v interface{}) ([]uint64, error)
    ParseValue(v interface{}) ([]uint64, error)
}
```

### 内置解析器

#### 1. CommonParser

通用解析器，支持字符串和数值。

```go
// 创建通用字符串解析器
func NewCommonStrParser() parser.FieldValueParser

// 创建通用数值解析器
func NewCommonNumberParser(f2i bool) parser.FieldValueParser
    // f2i: 是否将浮点数转换为整数
```

#### 2. HashStrParser

哈希字符串解析器。

```go
type HashStrParser struct {
    // 私有字段...
}

func NewHashStrParser() *HashStrParser
func (p *HashStrParser) Name() string
func (p *HashStrParser) ParseAssign(v interface{}) ([]uint64, error)
func (p *HashStrParser) ParseValue(v interface{}) ([]uint64, error)
```

#### 3. NumberParser

数值解析器。

```go
type NumberParser struct {
    // 私有字段...
}

func NewNumberParser() *NumberParser
func (p *NumberParser) Name() string
func (p *NumberParser) ParseAssign(v interface{}) ([]uint64, error)
func (p *NumberParser) ParseValue(v interface{}) ([]uint64, error)
```

#### 4. RangeParser

范围解析器，用于范围查询。

```go
type RangeParser struct {
    // 私有字段...
}

func NewRangeParser() *RangeParser
func (p *RangeParser) Name() string
func (p *RangeParser) ParseAssign(v interface{}) ([]uint64, error)
func (p *RangeParser) ParseValue(v interface{}) ([]uint64, error)
```

#### 5. GeoHashParser

地理哈希解析器。

```go
type GeoHashParser struct {
    precision int
}

func NewGeoHashParser(precision int) *GeoHashParser
func (p *GeoHashParser) Name() string
func (p *GeoHashParser) ParseAssign(v interface{}) ([]uint64, error)
func (p *GeoHashParser) ParseValue(v interface{}) ([]uint64, error)
```

**使用示例：**
```go
import "github.com/echoface/be_indexer/parser"

// 在roaringidx中使用解析器
builder := roaringidx.NewIndexerBuilder()
builder.ConfigureField("package", roaringidx.FieldSetting{
    Container: roaringidx.ContainerNameDefault,
    Parser:    parser.NewHashStrParser(),
})
```

### 解析器工具函数

```go
// 解析单个整数
func ParseIntegerNumber(v interface{}, f2i bool) (n int64, err error)

// 解析整数列表
func ParseIntergers(v interface{}, f2i bool) (res []int64, err error)
```

---

## 容器类型

### EntriesHolder

条目容器接口。

```go
type EntriesHolder interface {
    Name() string
    CreateHolder(desc *FieldDesc) EntriesHolder
    IndexingBETx(desc *FieldDesc, expr *BoolValues) (TxData, error)
    DecodeTxData(data []byte) (TxData, error)
}
```

### 容器注册

```go
type HolderBuilder func() EntriesHolder

// 创建容器实例
func NewEntriesHolder(name string) EntriesHolder

// 检查容器是否存在
func HasHolderBuilder(name string) bool

// 注册自定义容器
func RegisterEntriesHolder(name string, builder HolderBuilder)
```

### 内置容器

#### 1. DefaultEntriesHolder（默认容器）

```go
// 创建默认容器
func NewDefaultEntriesHolder() EntriesHolder

// 使用示例
builder.ConfigField("age", be_indexer.FieldOption{
    Container: be_indexer.HolderNameDefault,  // 或省略，默认就是default
})
```

#### 2. AhoCorasickMatcherHolder

AC自动机匹配容器，用于字符串模式匹配。

```go
// 创建AC匹配器容器
func NewAhoCorasickMatcherHolder() EntriesHolder

// 使用示例
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})
```

#### 3. ExtendRangeHolder

扩展范围容器。

```go
// 创建扩展范围容器
func NewExtendRangeHolder() EntriesHolder

// 使用示例
builder.ConfigField("score", be_indexer.FieldOption{
    Container: HolderNameExtendRange,
})
```

### AC自动机容器使用示例

```go
// 构建文档
doc := be_indexer.NewDocument(1)
doc.AddConjunction(
    be_indexer.NewConjunction().
        Include("keyword", be_indexer.NewStrValues("advertisement", "promotion")),
)

// 配置字段使用AC匹配器
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})

// 检索
assigns := map[be_indexer.BEField]be_indexer.Values{
    "keyword": be_indexer.NewStrValues("ad", "promo"),  // 会匹配"advertisement", "promotion"
}
```

---

## Roaringidx API

roaringidx是基于Roaring Bitmap的布尔索引实现，在文档数量大、特征数小的场景下性能更优。

### IvtBEIndexer

```go
type IvtBEIndexer struct {
    // 私有字段...
}

// 创建新的roaringidx索引器
func NewIvtBEIndexer() *IvtBEIndexer
```

### FieldSetting

```go
type FieldSetting struct {
    Parser    parser.FieldValueParser  // 解析器
    Container string                   // 容器名称
}
```

### IvtIndexerBuilder

```go
type IvtIndexerBuilder struct {
    // 私有字段...
}

// 创建索引构建器
func NewIndexerBuilder() *IvtIndexerBuilder

// 配置字段
func (b *IvtIndexerBuilder) ConfigureField(field string, setting FieldSetting) error

// 添加文档
func (b *IvtIndexerBuilder) AddDocuments(docs ...*be_indexer.Document) error

// 构建索引器
func (b *IvtIndexerBuilder) BuildIndexer() (*IvtBEIndexer, error)
```

### IvtScanner

```go
type IvtScanner struct {
    // 私有字段...
}

// 创建扫描器
func NewScanner(indexer *IvtBEIndexer) *IvtScanner

// 检索文档
func (s *IvtScanner) Retrieve(assign be_indexer.Assignments) (be_indexer.DocIDList, error)

// 获取原始结果
func (s *IvtScanner) GetRawResult() *roaring.Bitmap

// 格式化结果
func FormatBitMapResult(arr []uint64) string

// 重置扫描器
func (s *IvtScanner) Reset()
```

### ContainerName

```go
const (
    ContainerNameDefault = "default"
    ContainerNameAC      = "ac_matcher"
    ContainerNameRange   = "range"
)
```

### Roaringidx完整示例

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/parser"
    "github.com/echoface/be_indexer/roaringidx"
)

func main() {
    // 创建构建器
    builder := roaringidx.NewIndexerBuilder()

    // 配置字段
    _ = builder.ConfigureField("package", roaringidx.FieldSetting{
        Container: roaringidx.ContainerNameDefault,
        Parser:    parser.NewHashStrParser(),
    })
    _ = builder.ConfigureField("age", roaringidx.FieldSetting{
        Container: roaringidx.ContainerNameDefault,
        Parser:    parser.NewNumberParser(),
    })

    // 构建文档
    doc1 := be_indexer.NewDocument(1)
    doc1.AddConjunction(be_indexer.NewConjunction().
        Include("age", be_indexer.NewIntValues(10, 20, 100)).
        Exclude("package", be_indexer.NewStrValues("com.echoface.not")))

    // 添加文档
    builder.AddDocuments(doc1)

    // 构建索引器
    indexer, err := builder.BuildIndexer()
    if err != nil {
        panic(err)
    }

    // 创建扫描器
    scanner := roaringidx.NewScanner(indexer)

    // 检索
    docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
        "age":     []int64{12, 20},
        "package": []interface{}{"com.echoface.be", "com.echoface.not"},
    })
    if err != nil {
        panic(err)
    }

    fmt.Println("匹配的文档:", docs)
    fmt.Println("原始结果:", roaringidx.FormatBitMapResult(scanner.GetRawResult().ToArray()))

    // 重置扫描器
    scanner.Reset()
}
```

---

## 实用工具API

### ID类型工具

```go
// ConjID: 连接ID
type ConjID uint64

// 创建ConjID
func NewConjID(docID DocID, index, size int) ConjID

// 获取ConjID信息
func (id ConjID) Size() int
func (id ConjID) Index() int
func (id ConjID) DocID() DocID
func (id ConjID) String() string

// EntryID: 条目ID
type EntryID uint64

// 创建EntryID
func NewEntryID(id ConjID, incl bool) EntryID

// EntryID方法
func (entry EntryID) IsExclude() bool
func (entry EntryID) IsInclude() bool
func (entry EntryID) GetConjID() ConjID
func (entry EntryID) IsNULLEntry() bool
func (entry EntryID) DocString() string
```

### 打印和调试工具

```go
// 打印索引信息
func PrintIndexInfo(index BEIndex)

// 打印索引条目
func PrintIndexEntries(index BEIndex)

// 收集器工具
func PickCollector() *DocIDCollector
func PutCollector(c *DocIDCollector)
```

### 结果收集器

```go
type DocIDCollector struct {
    // 私有字段...
}

func NewDocIDCollector() *DocIDCollector
func (c *DocIDCollector) Collect(docID DocID)
func (c *DocIDCollector) Result() DocIDList
func (c *DocIDCollector) Reset()
```

---

## 错误处理

### 常见错误类型

1. **文档错误**
   - 空Conjunction列表
   - Conjunction数量超过256

2. **字段配置错误**
   - 重复配置字段
   - 未知的容器类型

3. **索引构建错误**
   - 值解析失败
   - ID溢出

### 错误处理策略

通过 `WithBadConjBehavior` 配置错误处理：

```go
// 1. 返回错误（默认）
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.ErrorBadConj),
)

// 2. 跳过错误的Conjunction
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
)

// 3. 触发panic
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.PanicBadConj),
)
```

### 最佳实践

```go
// 1. 使用SkipBadConj处理大量数据
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
)

// 2. 为所有字段提前配置
for field := range allFields {
    builder.ConfigField(field, be_indexer.FieldOption{
        Container: be_indexer.HolderNameDefault,
    })
}

// 3. 使用紧凑型构建器提高性能
builder := be_indexer.NewCompactIndexerBuilder()

// 4. 为重复的Conjunction使用缓存
builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithCacheProvider(myCache),
)
```

---

## 性能优化

### 1. 选择合适的构建器

```go
// 标准构建器（默认）
builder := be_indexer.NewIndexerBuilder()

// 紧凑型构建器（性能提升12%）
builder := be_indexer.NewCompactIndexerBuilder()
```

### 2. 选择合适的容器

- **Default**: 适用于大多数场景
- **ACMatcher**: 适用于字符串模式匹配
- **ExtendRange**: 适用于范围查询优化

```go
// 字符串模式匹配
builder.ConfigField("keyword", be_indexer.FieldOption{
    Container: be_indexer.HolderNameACMatcher,
})

// 范围查询优化
builder.ConfigField("score", be_indexer.FieldOption{
    Container: HolderNameExtendRange,
})
```

### 3. 使用缓存

```go
type MyCache struct {
    data map[ConjID][]byte
}

func (c *MyCache) Reset() {
    c.data = make(map[ConjID][]byte)
}

func (c *MyCache) Get(conjID ConjID) ([]byte, bool) {
    data, ok := c.data[conjID]
    return data, ok
}

func (c *MyCache) Set(conjID ConjID, data []byte) {
    c.data[conjID] = data
}

builder := be_indexer.NewIndexerBuilder(
    be_indexer.WithCacheProvider(&MyCache{data: make(map[ConjID][]byte)}),
)
```

### 4. 字段配置最佳实践

- 提前配置所有字段，避免运行时动态配置
- 根据数据特点选择合适的容器和解析器
- 对于高频查询字段，考虑使用专门的容器

### 5. 检索优化

```go
// 只获取结果
result, err := indexer.Retrieve(assigns)

// 获取详细信息（调试用）
result, err := indexer.Retrieve(assigns,
    be_indexer.WithStepDetail(),
    be_indexer.WithDumpEntries())

// 使用自定义收集器
collector := be_indexer.NewDocIDCollector()
err := indexer.RetrieveWithCollector(assigns, collector)
result := collector.Result()
```

### 6. 内存优化

- 使用roaringidx处理大规模文档
- 定期调用 `PutCollector()` 回收收集器
- 对于roaringidx，考虑文档ID范围限制 [-2^56, 2^56]

---

## 完整使用示例

### 示例1: 基本用法

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    // 1. 创建构建器
    builder := be_indexer.NewIndexerBuilder(
        be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
    )

    // 2. 构建文档
    docs := []*be_indexer.Document{
        be_indexer.NewDocument(1).AddConjunction(
            be_indexer.NewConjunction().
                Include("age", be_indexer.NewIntValues(18, 25)).
                Include("city", be_indexer.NewStrValues("beijing")),
        ),
        be_indexer.NewDocument(2).AddConjunction(
            be_indexer.NewConjunction().
                GreatThan("age", 30).
                Include("vip", be_indexer.NewStrValues("true")),
        ),
        be_indexer.NewDocument(3).AddConjunction(
            be_indexer.NewConjunction().
                Include("age", be_indexer.NewIntValues(25)).
                Exclude("city", be_indexer.NewStrValues("rural")),
        ),
    }

    // 3. 添加文档
    err := builder.AddDocument(docs...)
    if err != nil {
        panic(err)
    }

    // 4. 构建索引
    indexer := builder.BuildIndex()

    // 5. 检索
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "age":  be_indexer.NewIntValues(25),
        "city": be_indexer.NewStrValues("beijing"),
    }

    result, err := indexer.Retrieve(assigns)
    if err != nil {
        panic(err)
    }

    fmt.Println("匹配文档:", result)
    // 输出: 匹配文档: [1]
}
```

### 示例2: 范围查询

```go
// 构建包含范围条件的文档
doc := be_indexer.NewDocument(10)
doc.AddConjunction(
    be_indexer.NewConjunction().
        GreatThan("score", 80).
        LessThan("age", 60).
        Between("price", 100, 1000),
)

// 检索
assigns := map[be_indexer.BEField]be_indexer.Values{
    "score": be_indexer.NewIntValues(90),
    "age":   be_indexer.NewIntValues(30),
    "price": be_indexer.NewIntValues(500),
}

result, err := indexer.Retrieve(assigns)
```

### 示例3: 使用Roaringidx

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/parser"
    "github.com/echoface/be_indexer/roaringidx"
)

func main() {
    builder := roaringidx.NewIndexerBuilder()

    // 配置字段
    _ = builder.ConfigureField("package", roaringidx.FieldSetting{
        Container: roaringidx.ContainerNameDefault,
        Parser:    parser.NewHashStrParser(),
    })
    _ = builder.ConfigureField("age", roaringidx.FieldSetting{
        Container: roaringidx.ContainerNameDefault,
        Parser:    parser.NewNumberParser(),
    })

    // 构建文档
    doc1 := be_indexer.NewDocument(1)
    doc1.AddConjunction(be_indexer.NewConjunction().
        Include("age", be_indexer.NewIntValues(10, 20, 100)).
        Exclude("package", be_indexer.NewStrValues("com.echoface.not")))

    builder.AddDocuments(doc1)

    indexer, err := builder.BuildIndexer()
    if err != nil {
        panic(err)
    }

    scanner := roaringidx.NewScanner(indexer)
    docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
        "age":     []int64{12, 20},
        "package": []interface{}{"com.echoface.be"},
    })

    fmt.Println("文档:", docs)
    scanner.Reset()
}
```

---

## 总结

be_indexer提供了灵活的布尔表达式索引解决方案，适用于：

- **广告投放系统**: 基于用户特征匹配广告规则
- **商品推荐系统**: 基于商品属性匹配用户偏好
- **规则引擎**: 复杂业务规则的匹配和检索
- **内容检索**: 基于标签、关键词的内容筛选

选择合适的实现（默认索引器 vs roaringidx）、配置合适的容器和解析器，可以获得最佳的性能表现。
