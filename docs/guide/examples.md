# be_indexer 示例集合

基础示例与 full/delta 示例有可执行版本：[example_test.go](../../example_test.go)，运行 `go test . -run Example`。

本文档包含了 `be_indexer` (读写分离/mmap版) 的各种使用示例，涵盖常见场景和高级用法。

## 目录

1. [基础示例：构建与检索](#基础示例构建与检索)
2. [广告定向系统](#广告定向系统)
3. [多模式子串匹配 (AC自动机)](#多模式子串匹配-ac自动机)
4. [高级排异 (Exclude) 与 Z-Entry](#高级排异-exclude-与-z-entry)
5. [全量+增量索引与在线服务](#全量增量索引与在线服务)

---

## 基础示例：构建与检索

在这个示例中，我们将演示如何将布尔规则文档导出为物理段文件，然后将其加载到检索引擎中进行匹配。

```go
package main

import (
    "bytes"
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
	fields := be_indexer.Schema{
		"age":  {IndexType: be_indexer.IndexNameExtendRange, Encoder: "ext_range"},
		"city": {IndexType: be_indexer.IndexNameDefault},
	}
	docs := []*be_indexer.Document{
		be_indexer.NewDocument(1).AddConjunction(be_indexer.NewConjunction().Between("age", 18, 25).Include("city", "beijing")),
		be_indexer.NewDocument(2).AddConjunction(be_indexer.NewConjunction().Exclude("city", "rural")),
	}
	var buf bytes.Buffer
	if err := be_indexer.BuildSegment(&buf, fields, docs, be_indexer.BuildSegmentOptions{}); err != nil {
		panic(err)
	}
	reader, err := be_indexer.NewSegmentReaderWithOptions(buf.Bytes(), be_indexer.SegmentReaderOptions{
		BlockChecksumMode: be_indexer.BlockChecksumOnOpen,
	})
	if err != nil {
		panic(err)
	}
	eng, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
	if err != nil {
		_ = reader.Close()
		panic(err)
	}
	defer eng.Close()
	query := be_indexer.Assignments{"age": 20, "city": "beijing"}
	collector := be_indexer.NewDocIDCollector() // This worker owns and reuses it.
	collector.Reset()
	if err := eng.RetrieveWithCollector(query, collector, be_indexer.WithStrictQuery()); err != nil {
		panic(err)
	}
	owned := collector.Bitmap().Clone()
	collector.Reset()
	fmt.Println(owned.Contains(1), owned.Contains(2), collector.DocCount())

	// K=0 still obeys its Exclude constraint.
	if err := eng.RetrieveWithCollector(be_indexer.Assignments{"city": "rural"}, collector); err != nil {
		panic(err)
	}
	fmt.Println(collector.DocCount())
	// Output:
	// true true 0
	// 0
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
    fieldsMeta := core.Schema{
        "age":    {IndexType: core.IndexNameExtendRange, Encoder: "ext_range"},
        "city":   {IndexType: core.IndexNameDefault, Encoder: "default"},
        "is_vip": {IndexType: core.IndexNameDefault, Encoder: "default"},
    }

    // 定义广告定向规则
    adRules := []*core.Document{
        // 广告1001: 北京上海 18-25岁 VIP用户
        core.NewDocument(1001).AddConjunction(
            core.NewConjunction().
                Between("age", 18, 25).
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
    if err := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, adRules, builder.BuildSegmentFromDocsOptions{}); err != nil { panic(err) }

    // 加载引擎
    segReader, err := segment.NewSegmentReader(segBuf.Bytes())
    if err != nil { panic(err) }
    searcher, err := engine.NewBooleanEngine(fieldsMeta, []*segment.SegmentReader{segReader})
    if err != nil {
        _ = segReader.Close()
        panic(err)
    }
    defer searcher.Close()

    // 模拟用户画像请求
    userProfiles := []core.Assignments{
        // 用户A: 22岁，北京，VIP
        {"age": []int{22}, "city": []string{"beijing"}, "is_vip": []string{"true"}},
        // 用户B: 28岁，农村，非VIP
        {"age": []int{28}, "city": []string{"rural"}, "is_vip": []string{"false"}},
    }

    for i, profile := range userProfiles {
        result, err := searcher.Retrieve(profile)
        if err != nil { panic(err) }
        fmt.Printf("用户%d 命中的广告数: %d\n", i+1, result.Cardinality())
    }
    // 输出:
    // 用户1 命中的广告数: 1
    // 用户2 命中的广告数: 0  (广告1002因为农村被 Exclude 排除)
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
    fieldsMeta := core.Schema{
        "content": {IndexType: core.IndexNameACMatcher, Encoder: core.IndexNameACMatcher},
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
    if err := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, rules, builder.BuildSegmentFromDocsOptions{}); err != nil { panic(err) }

    segReader, err := segment.NewSegmentReader(segBuf.Bytes())
    if err != nil { panic(err) }
    searcher, err := engine.NewBooleanEngine(fieldsMeta, []*segment.SegmentReader{segReader})
    if err != nil {
        _ = segReader.Close()
        panic(err)
    }
    defer searcher.Close()

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

        result, err := searcher.Retrieve(query)
        if err != nil { panic(err) }
        fmt.Printf("文本 %d 命中规则数: %d\n", i+1, result.Cardinality())
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
    fieldsMeta := core.Schema{
        "device": {IndexType: core.IndexNameDefault, Encoder: "default"},
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
    if err := builder.BuildSegmentFromDocs(&segBuf, fieldsMeta, rules, builder.BuildSegmentFromDocsOptions{}); err != nil { panic(err) }

    segReader, err := segment.NewSegmentReader(segBuf.Bytes())
    if err != nil { panic(err) }
    searcher, err := engine.NewBooleanEngine(fieldsMeta, []*segment.SegmentReader{segReader})
    if err != nil {
        _ = segReader.Close()
        panic(err)
    }
    defer searcher.Close()

    // 测试不同设备的命中情况
    queryAndroid := core.Assignments{"device": []string{"android"}}
    queryIOS := core.Assignments{"device": []string{"ios"}}

    res1, err := searcher.Retrieve(queryAndroid)
    if err != nil { panic(err) }
    res2, err := searcher.Retrieve(queryIOS)
    if err != nil { panic(err) }

    fmt.Println("Android 命中数:", res1.Cardinality()) // 输出: 1
    fmt.Println("iOS 命中数:", res2.Cardinality())     // 输出: 0
}
```

---

## 全量+增量索引与在线服务

本示例展示推荐的生产流程：全量构建 → 构建一个累计 delta → Manifest 发布 → 在线 mmap 服务。每轮 delta 包含当前 full 之后的全部累计变更，并替换上一轮 delta；一个 full 或 delta 均可包含多个 segment。发布与可选评估见[最佳实践](best-practices.md#10-索引合并与全量发布评估)。

```go
package main

import (
    "fmt"
    "os"
    "github.com/echoface/be_indexer"
)

func main() {
	root, err := os.MkdirTemp("", "be-indexer-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	fields := be_indexer.Schema{"city": {IndexType: be_indexer.IndexNameDefault}}
	full, err := be_indexer.NewFullIndexBuilder(be_indexer.FullIndexBuildOptions{
		Root: root, Generation: 1, SnapshotWatermark: 10, Fields: fields,
	})
	if err != nil {
		panic(err)
	}
	defer full.Close()
	if err := full.AddDocument(be_indexer.NewDocument(1).AddConjunction(be_indexer.NewConjunction().Include("city", "bj"))); err != nil {
		panic(err)
	}
	fullDesc, err := full.Build()
	if err != nil {
		panic(err)
	}
	delta, err := be_indexer.NewDeltaIndexBuilder(be_indexer.DeltaIndexBuildOptions{
		Root: root, Generation: 2, FromWatermarkExclusive: 10, ToWatermarkInclusive: 11, Fields: fields,
	})
	if err != nil {
		panic(err)
	}
	defer delta.Close()
	if err := delta.AddMutation(be_indexer.Mutation{DocID: 1, Version: 1, Op: be_indexer.MutationDelete}); err != nil {
		panic(err)
	}
	if err := delta.AddMutation(be_indexer.Mutation{
		DocID: 2, Version: 1, Op: be_indexer.MutationUpsert,
		Document: be_indexer.NewDocument(2).AddConjunction(be_indexer.NewConjunction().Include("city", "bj")),
	}); err != nil {
		panic(err)
	}
	deltaDesc, err := delta.Build()
	if err != nil {
		panic(err)
	}
	m, err := be_indexer.NewSnapshotManifest(be_indexer.SnapshotManifestRequest{
		IndexName: "example", Generation: 2, Fields: fields,
		Full: fullDesc, Deltas: []be_indexer.DeltaIndexDescriptor{deltaDesc},
	})
	if err != nil {
		panic(err)
	}
	if err := be_indexer.PublishManifest(root, "manifest-2.json", m); err != nil {
		panic(err)
	}
	holder, err := be_indexer.NewIndexHolder(root, fields, be_indexer.LoaderOptions{SegmentLoad: be_indexer.SegmentLoadMmapVerify})
	if err != nil {
		panic(err)
	}
	defer holder.Close()
	result, err := holder.Retrieve(be_indexer.Assignments{"city": "bj"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Contains(1), result.Contains(2))
	// Output: false true
}
```

生产中将示例的临时 root 换为持久目录，构建并发布新代后调用 `holder.Reload()`。失败时旧快照继续服务；查询结束后旧 mmap 才被回收。发布目录、Manifest 和加载模式的详细契约见 [最佳实践](best-practices.md)。

查询结果为 `(FullResult − ChangedDocs) ∪ (可见 DeltaResults − DeletedDocs)`。loader 会给较旧 delta 应用 DocVisibilityMask，以屏蔽后续 delta 已更新的 DocID。
