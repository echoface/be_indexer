# be_indexer 示例集合

本文档包含了 `be_indexer` (读写分离/mmap版) 的各种使用示例，涵盖常见场景和高级用法。

## 目录

1. [基础示例：构建与检索](#基础示例构建与检索)
2. [广告定向系统](#广告定向系统)
3. [多模式子串匹配 (AC自动机)](#多模式子串匹配-ac自动机)
4. [高级排异 (Exclude) 与 Z-Entry](#高级排异-exclude-与-z-entry)

---

## 基础示例：构建与检索

在这个示例中，我们将演示如何将布尔规则文档导出为物理段文件，然后将其加载到检索引擎中进行匹配。

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

    // 2. 构造业务规则文档 (DNF范式)
    docs := []*core.Document{}
    
    // Doc 1: age IN [18, 25] AND city IN ["beijing"]
    doc1 := core.NewDocument(1)
    conj1 := core.NewConjunction()
    conj1.In("age", core.NewIntValues(18, 25))
    conj1.In("city", core.NewStrValues("beijing"))
    doc1.AddConjunction(conj1)
    docs = append(docs, doc1)

    // 3. 构建物理段
    file, _ := os.Create("base.seg")
    wildcards, err := builder.BuildSegmentFromDocs(file, fieldsMeta, docs)
    if err != nil {
        panic(err)
    }
    file.Close()

    // 4. 零拷贝加载
    fileData, _ := os.ReadFile("base.seg") // 生产推荐使用 syscall.Mmap
    segReader, err := segment.NewMmapReader(fileData)
    if err != nil {
        panic(err)
    }

    // 5. 初始化查询引擎
    searcher := engine.NewBooleanEngine(fieldsMeta, wildcards, []*segment.MmapReader{segReader})

    // 6. 执行检索
    assigns := core.Assignments{
        "age":  []int{20},
        "city": []string{"beijing"},
    }

    result, err := searcher.Retrieve(assigns)
    if err != nil {
        panic(err)
    }
    
    fmt.Println("匹配文档:", result) // 输出: [1]
}
```

---

## 广告定向系统

在广告系统中，一条广告往往具有多重维度的包含与排除条件。

```go
package main

import (
    "fmt"
    "bytes"
    "github.com/echoface/be_indexer/core"
    "github.com/echoface/be_indexer/builder"
    "github.com/echoface/be_indexer/segment"
    "github.com/echoface/be_indexer/engine"
)

func main() {
    fieldsMeta := map[core.BEField]*core.FieldMeta{
        "age":    {Field: "age", ID: 1, FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "number"}},
        "city":   {Field: "city", ID: 2, FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "default"}},
        "is_vip": {Field: "is_vip", ID: 3, FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "default"}},
    }

    // 定义广告定向规则
    adRules := []*core.Document{
        // 广告1001: 北京上海 18-25岁 VIP用户
        core.NewDocument(1001).AddConjunction(
            core.NewConjunction().
                In("age", core.NewIntValues(18, 25)).
                In("city", core.NewStrValues("beijing", "shanghai")).
                In("is_vip", core.NewStrValues("true")),
        ),
        // 广告1002: 非VIP用户，排除农村地区
        core.NewDocument(1002).AddConjunction(
            core.NewConjunction().
                In("is_vip", core.NewStrValues("false")).
                NotIn("city", core.NewStrValues("rural")),
        ),
    }

    // 编译到内存 Buffer 中模拟落盘
    var segBuf bytes.Buffer
    wildcards, _ := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, adRules)

    // 加载引擎
    segReader, _ := segment.NewMmapReader(segBuf.Bytes())
    searcher := engine.NewBooleanEngine(fieldsMeta, wildcards, []*segment.MmapReader{segReader})

    // 模拟用户画像请求
    userProfiles := []core.Assignments{
        // 用户A: 22岁，北京，VIP
        {"age": []int{22}, "city": []string{"beijing"}, "is_vip": []string{"true"}},
        // 用户B: 28岁，农村，非VIP
        {"age": []int{28}, "city": []string{"rural"}, "is_vip": []string{"false"}},
    }

    for i, profile := range userProfiles {
        result, _ := searcher.Retrieve(profile)
        fmt.Printf("用户%d 命中的广告: %v\n", i+1, result)
    }
    // 输出:
    // 用户1 命中的广告: [1001]
    // 用户2 命中的广告: []  (广告1002因为农村被 Exclude 排除)
}
```

---

## 多模式子串匹配 (AC自动机)

利用内置的 `ACMatcher` 容器，我们可以实现高性能的子串匹配。

```go
package main

import (
    "fmt"
    "bytes"
    "github.com/echoface/be_indexer/core"
    "github.com/echoface/be_indexer/builder"
    "github.com/echoface/be_indexer/segment"
    "github.com/echoface/be_indexer/engine"
)

func main() {
    // 配置 ACMatcher
    fieldsMeta := map[core.BEField]*core.FieldMeta{
        "content": {
            Field: "content", 
            ID: 1, 
            FieldOption: core.FieldOption{
                Container: core.IndexNameACMatcher,
            },
        },
    }

    // 规则库: 只要包含了特定关键词即命中规则
    rules := []*core.Document{
        // 促销规则
        core.NewDocument(8001).AddConjunction(
            core.NewConjunction().
                In("content", core.NewStrValues("advertisement", "promotion", "marketing")),
        ),
        // 敏感词规则
        core.NewDocument(8002).AddConjunction(
            core.NewConjunction().
                In("content", core.NewStrValues("spam", "fraud")),
        ),
    }

    var segBuf bytes.Buffer
    wildcards, _ := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, rules)

    segReader, _ := segment.NewMmapReader(segBuf.Bytes())
    searcher := engine.NewBooleanEngine(fieldsMeta, wildcards, []*segment.MmapReader{segReader})

    // 测试长文本匹配
    texts := []string{
        "This is an amazing promotion event!",  // 应该匹配促销规则
        "Warning: potential fraud detected.",   // 应该匹配敏感规则
        "Just a normal message.",               // 不匹配任何规则
    }

    for i, text := range texts {
        query := core.Assignments{
            "content": []string{text}, // AC Matcher 会将整个 text 去匹配所有的 rules
        }

        result, _ := searcher.Retrieve(query)
        fmt.Printf("文本 %d 命中规则: %v\n", i+1, result)
    }
}
```

---

## 高级排异 (Exclude) 与 Z-Entry

在业务中，有时规则没有包含条件，而**只有排除条件**。`be_indexer` 能够在 K=0 组优雅地处理这种情况。

```go
package main

import (
    "fmt"
    "bytes"
    "github.com/echoface/be_indexer/core"
    "github.com/echoface/be_indexer/builder"
    "github.com/echoface/be_indexer/segment"
    "github.com/echoface/be_indexer/engine"
)

func main() {
    fieldsMeta := map[core.BEField]*core.FieldMeta{
        "device": {Field: "device", ID: 1, FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "default"}},
    }

    rules := []*core.Document{
        // 规则 1: 只要不是 iOS 就展示 (纯 Exclude)
        core.NewDocument(9001).AddConjunction(
            core.NewConjunction().
                NotIn("device", core.NewStrValues("ios")),
        ),
    }

    var segBuf bytes.Buffer
    // Wildcards 将会捕获这个纯 Exclude 规则
    wildcards, _ := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, rules)

    segReader, _ := segment.NewMmapReader(segBuf.Bytes())
    searcher := engine.NewBooleanEngine(fieldsMeta, wildcards, []*segment.MmapReader{segReader})

    // 测试不同设备的命中情况
    queryAndroid := core.Assignments{"device": []string{"android"}}
    queryIOS := core.Assignments{"device": []string{"ios"}}

    res1, _ := searcher.Retrieve(queryAndroid)
    res2, _ := searcher.Retrieve(queryIOS)

    fmt.Println("Android 命中:", res1) // 输出: [9001]
    fmt.Println("iOS 命中:", res2)     // 输出: []
}
```
