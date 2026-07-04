# 快速入门指南

本指南将帮助您快速上手 `be_indexer`，利用最新的读写分离与 mmap 零拷贝架构，实现千万级布尔规则的高性能匹配。

## 目录

1. [核心概念](#核心概念)
2. [环境要求](#环境要求)
3. [三步构建你的第一个检索引擎](#三步构建你的第一个检索引擎)
4. [常见问题 (FAQ)](#常见问题)

---

## 核心概念

在使用新版 `be_indexer` 之前，您只需要理解三个简单的领域包划分：

1. **`builder` (编译层)**：将人类和业务可读的 `Document` 列表转化为高度压缩的二进制物理段文件 (`.seg`)。
2. **`segment` (存储层)**：封装了底层的二进制流，通过 `MmapReader` 将文件以零拷贝的形式映射到内存中。
3. **`engine` (执行层)**：也就是 `BooleanEngine`，它接收用户的查询特征 (`Assignments`)，在 `MmapReader` 提供的底层数据上执行飞速的条件求交。

---

## 环境要求

- Go 1.18+ (推荐使用 1.20+)
- 支持 `mmap` 的操作系统 (Linux, macOS, Unix-like)

```bash
go get github.com/echoface/be_indexer
```

---

## 三步构建你的第一个检索引擎

我们将模拟一个简单的商品定向投放场景。

### 第一步：定义特征字典与规则文档

首先，我们需要告诉引擎有哪些字段，以及它们的解析方式。
然后，我们将业务规则包装为 `core.Document` 列表。

```go
package main

import (
    "fmt"
    "os"
    "github.com/echoface/be_indexer/core"
    "github.com/echoface/be_indexer/builder"
    "github.com/echoface/be_indexer/segment"
    "github.com/echoface/be_indexer/engine"
)

func main() {
    // 1. 定义字段元数据
    fieldsMeta := map[core.BEField]*core.FieldMeta{
        "age": {
            Field: "age", ID: 1, 
            FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "number"},
        },
        "city": {
            Field: "city", ID: 2, 
            FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "default"},
        },
    }

    // 2. 构造商品可见性规则
    // 商品A(ID: 1): 只投放给 18-25岁 的 北京 用户
    docA := core.NewDocument(1)
    conjA := core.NewConjunction()
    conjA.In("age", core.NewIntValues(18, 25))
    conjA.In("city", core.NewStrValues("beijing"))
    docA.AddConjunction(conjA)

    // 商品B(ID: 2): 只要不是 农村 的用户都能看 (这是一个纯 Exclude 规则)
    docB := core.NewDocument(2)
    conjB := core.NewConjunction()
    conjB.NotIn("city", core.NewStrValues("rural"))
    docB.AddConjunction(conjB)

    docs := []*core.Document{docA, docB}
```

### 第二步：编译为物理段文件

我们将刚才的规则列表转为紧凑的二进制段。在生产中，这通常发生在一个分布式的离线 Hadoop/Spark 任务或者单独的 Builder 进程中。

```go
    // 3. 创建物理文件并导出
    file, err := os.Create("goods.seg")
    if err != nil {
        panic(err)
    }
    
    // wildcards 包含了 K=0 (如只有排除条件) 的特殊项，需要被引擎妥善保管
    wildcards, err := builder.BuildSegmentFromDocs(file, fieldsMeta, docs)
    if err != nil {
        panic(err)
    }
    file.Close()
```

### 第三步：零拷贝加载与在线检索

现在我们来到了在线检索进程（比如一个高并发的 Web API 服务）。

```go
    // 4. 零拷贝加载物理段 (生产环境中推荐使用 syscall.Mmap 以获得真正的 Zero-Copy)
    fileData, err := os.ReadFile("goods.seg")
    if err != nil {
        panic(err)
    }
    segReader, err := segment.NewMmapReader(fileData)
    if err != nil {
        panic(err)
    }

    // 5. 初始化在线查询引擎
    searcher := engine.NewBooleanEngine(fieldsMeta, wildcards, []*segment.MmapReader{segReader})

    // 6. 模拟用户请求并执行检索
    
    // 场景1: 20岁的北京用户
    req1 := core.Assignments{
        "age":  []int{20},
        "city": []string{"beijing"},
    }
    res1, _ := searcher.Retrieve(req1)
    fmt.Println("20岁北京用户 可见的商品:", res1) // 应该匹配商品A 和 商品B(不是农村) -> [1, 2]

    // 场景2: 40岁的农村用户
    req2 := core.Assignments{
        "age":  []int{40},
        "city": []string{"rural"},
    }
    res2, _ := searcher.Retrieve(req2)
    fmt.Println("40岁农村用户 可见的商品:", res2) // 商品A年龄不符，商品B排除了农村 -> []
}
```

---

## 常见问题 (FAQ)

### 1. 什么是 `wildcards`？为什么引擎需要它？
`builder.BuildSegmentFromDocs` 除了生成二进制文件，还会返回一个 `wildcards` 数组。
这是因为存在一些**没有任何包含条件**（如纯 Exclude 规则，或什么条件都没配置）的规则。这些规则在查询时不需要走底层倒排链的提取，而是引擎在处理前置 K=0 的层级时直接介入的。因此在分发 `.seg` 文件时，请务必连同这部分 `wildcards` 一并分发给查询节点（可以序列化为旁路的 JSON）。

### 2. 为什么我在段文件中看不到原来的字符串特征了？
物理段内部的 `FlatDict` 和 `FlatPostingList` 是高度汇编化的。所有字符串都在编译阶段被去重、哈希/排序，被转化为了位元结构的 `EntryID`，这是 `be_indexer` 性能极高的秘诀之一。

### 3. 如何动态更新或者删除规则？
新版架构中 Segment 是完全不可变的（Immutable）。
若要实现动态删除，请使用 `engine` 暴露的 **LiveDocs** 机制：
```go
liveDocs := core.NewLiveDocs()
liveDocs.Add(1) // 标记文档 1 为存活
// 文档 2 被移除
searcher.SetLiveDocs(liveDocs)
```
检索时，引擎会自动过滤掉未在 `LiveDocs` 中的 `DocID`。增量的新文档则可以编译为一个新的 `.seg`，作为新的 `MmapReader` 挂载到 `BooleanEngine` 的 `segments` 数组中。

---

更多高阶功能（如 AC多模式匹配，紧凑 ID 编解码），请参考 [API 参考文档](./API_REFERENCE.md) 和 [设计原理](./ARCHITECTURE.md)。
