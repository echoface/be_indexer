# Boolean Expression Indexer - API Reference

## 目录

1. [概述](#概述)
2. [核心模型 (core)](#核心模型-core)
3. [离线构建 API (builder)](#离线构建-api-builder)
4. [物理段 API (segment)](#物理段-api-segment)
5. [检索执行 API (engine)](#检索执行-api-engine)

---

## 概述

be_indexer 是一个高性能的布尔表达式索引引擎，它在最新的架构中被划分为四个核心领域包：
- `core`: 基础原语与结构定义。
- `builder`: 将逻辑文档编译为扁平的物理段。
- `segment`: 管理底层存储与零拷贝 `mmap` 的读写。
- `engine`: 基于 K-Groups 算法进行多路归并求交。

---

## 核心模型 (core)

### Document

文档结构，表示一个可索引的布尔规则集合（DNF 范式，各 Conjunction 之间是 OR 关系）。

```go
type Document struct {
    ID   DocID          `json:"id"`   // 文档ID
    Cons []*Conjunction `json:"cons"` // 布尔表达式列表（OR关系）
}

// 创建新文档
func NewDocument(id DocID) *Document

// 添加Conjunction
func (doc *Document) AddConjunction(cons ...*Conjunction) *Document
```

### Conjunction

表示一个 AND 表达式组。包含多个 Predicate 约束。

```go
type Conjunction struct {
    // 内部结构
}

// 创建新Conjunction
func NewConjunction() *Conjunction

// 添加包含条件 (In)
func (conj *Conjunction) In(field BEField, values Values) *Conjunction

// 添加排除条件 (NotIn)
func (conj *Conjunction) NotIn(field BEField, values Values) *Conjunction
```

### FieldMeta

定义字段的属性和所使用的底层存储容器（如默认的哈希，或是 AC 自动机）。

```go
type FieldMeta struct {
    ID        uint64
    Field     BEField
    Container string // core.IndexNameDefault 或 core.IndexNameACMatcher
    Tokenizer string // 解析器名称（如 "number", "default"）
}
```

### Assignments

查询时的属性赋值，将字段映射到用户特征上。

```go
type Assignments map[BEField]interface{}
```

---

## 离线构建 API (builder)

### BuildSegmentFromDocs

这是构建过程的唯一核心入口，负责将内存中的 `Document` 列表转换为二进制的紧凑物理段。

```go
// BuildSegmentFromDocs exports documents directly into a memory-mappable segment
// 返回生成的 wildcard entries（K=0 或者是纯 Exclude 的条件），这些需要被引擎外挂加载。
func BuildSegmentFromDocs(
    w io.Writer, 
    fieldsData map[core.BEField]*core.FieldMeta, 
    docs []*core.Document,
) (core.Entries, error)
```

**示例：**
```go
fieldsMeta := map[core.BEField]*core.FieldMeta{
    "age": {Field: "age", ID: 1, Container: core.IndexNameDefault},
}

docs := []*core.Document{ /* ... */ }
file, _ := os.Create("data.seg")
defer file.Close()

wildcards, err := builder.BuildSegmentFromDocs(file, fieldsMeta, docs)
```

---

## 物理段 API (segment)

`segment` 包是对底层二进制文件的包装，核心面向检索端暴露的是 `SegmentReader`。

### SegmentReader

使用零拷贝方式映射并读取段文件内容。

```go
type SegmentReader struct {
    // 内部结构
}

// 从字节切片（通常是 mmap 的结果）初始化 Reader
func NewSegmentReader(data []byte) (*SegmentReader, error)

// 从文件路径通过 mmap 加载（推荐生产使用）
func OpenSegmentFile(path string, opts ...SegmentReaderOption) (*SegmentReader, error)
```

> **注意：** 在生产环境中，推荐使用 `OpenSegmentFile` 直接通过 mmap 加载 `.seg` 文件，零拷贝读取所有 posting/wildcard 数据。

---

## 检索执行 API (engine)

`engine` 包是查询的大脑，它组合底层的 Segment 和外置的 Wildcards 提供毫秒级的布尔检索能力。

### BooleanEngine

核心检索引擎。

```go
type BooleanEngine struct {
    // 内部结构
}

// 初始化引擎
// fieldsData: 字段元数据定义
// segments: 可挂载多个 SegmentReader，引擎内部会透明处理合并
func NewBooleanEngine(
    fieldsData map[core.BEField]*core.FieldMeta, 
    segments []*segment.SegmentReader,
) *BooleanEngine

// 指定 LiveDocs 以支持动态删除/禁用文档
func (ms *BooleanEngine) SetLiveDocs(ld *core.LiveDocs)

// 基础 Retrieve 方法
func (ms *BooleanEngine) Retrieve(queries core.Assignments, opts ...core.IndexOpt) (core.DocIDList, error)

// 带自定义结果收集器的 Retrieve
func (ms *BooleanEngine) RetrieveWithCollector(queries core.Assignments, collector core.ResultCollector, opts ...core.IndexOpt) error
```

**检索示例：**
```go
// 1. 初始化引擎
searcher := engine.NewBooleanEngine(fieldsMeta, []*segment.SegmentReader{segReader})

// 2. 构造查询特征
assigns := core.Assignments{
    "age": []int{20, 25},
    "city": []string{"shanghai"},
}

// 3. 执行检索
docIDs, err := searcher.Retrieve(assigns)
if err != nil {
    panic(err)
}

fmt.Printf("Matched Docs: %v\n", docIDs)
```
