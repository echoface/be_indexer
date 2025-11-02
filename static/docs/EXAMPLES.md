# 示例集合

本文档包含了be_indexer的各种使用示例，涵盖常见场景和高级用法。

## 目录

1. [基础示例](#基础示例)
2. [广告投放系统](#广告投放系统)
3. [电商筛选系统](#电商筛选系统)
4. [内容推荐系统](#内容推荐系统)
5. [地理信息查询](#地理信息查询)
6. [roaringidx示例](#roaringidx示例)
7. [AC自动机示例](#ac自动机示例)
8. [自定义容器示例](#自定义容器示例)
9. [缓存使用示例](#缓存使用示例)
10. [性能测试示例](#性能测试示例)

---

## 基础示例

### 示例1: 基本用法

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
    }

    // 3. 添加文档
    builder.AddDocument(docs...)

    // 4. 构建索引
    indexer := builder.BuildIndex()

    // 5. 检索
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "age": be_indexer.NewIntValues(20),
        "city": be_indexer.NewStrValues("beijing"),
    }

    result, _ := indexer.Retrieve(assigns)
    fmt.Println("匹配文档:", result)
}
```

### 示例2: 紧凑型索引器

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    // 使用紧凑型构建器（性能提升12%）
    builder := be_indexer.NewCompactIndexerBuilder()

    // 构建文档
    doc := be_indexer.NewDocument(1)
    doc.AddConjunction(
        be_indexer.NewConjunction().
            Include("category", be_indexer.NewStrValues("electronics")).
            GreatThan("price", 100),
    )

    builder.AddDocument(doc)
    indexer := builder.BuildIndex()

    // 检索
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "category": be_indexer.NewStrValues("electronics"),
        "price": be_indexer.NewIntValues(200),
    }

    result, _ := indexer.Retrieve(assigns)
    fmt.Println("匹配文档:", result)
}
```

---

## 广告投放系统

### 示例3: 多维度广告投放规则

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 定义广告投放规则
    adRules := []*be_indexer.Document{
        // 规则1: 北京上海18-25岁VIP用户
        be_indexer.NewDocument(1001).AddConjunction(
            be_indexer.NewConjunction().
                Include("age", be_indexer.NewIntValues(18, 25)).
                Include("city", be_indexer.NewStrValues("beijing", "shanghai")).
                Include("is_vip", be_indexer.NewStrValues("true")),
        ),
        // 规则2: 非VIP用户排除农村
        be_indexer.NewDocument(1002).AddConjunction(
            be_indexer.NewConjunction().
                Include("is_vip", be_indexer.NewStrValues("false")).
                Exclude("city", be_indexer.NewStrValues("rural")),
        ),
        // 规则3: 年龄大于30岁高消费用户
        be_indexer.NewDocument(1003).AddConjunction(
            be_indexer.NewConjunction().
                GreatThan("age", 30).
                GreatThan("consumption", 10000),
        ),
    }

    builder.AddDocument(adRules...)
    indexer := builder.BuildIndex()

    // 用户画像
    userProfiles := []map[be_indexer.BEField]be_indexer.Values{
        // 用户1: 22岁，北京，VIP
        {
            "age": be_indexer.NewIntValues(22),
            "city": be_indexer.NewStrValues("beijing"),
            "is_vip": be_indexer.NewStrValues("true"),
        },
        // 用户2: 35岁，上海，普通用户
        {
            "age": be_indexer.NewIntValues(35),
            "city": be_indexer.NewStrValues("shanghai"),
            "is_vip": be_indexer.NewStrValues("false"),
            "consumption": be_indexer.NewIntValues(15000),
        },
        // 用户3: 28岁，农村，非VIP
        {
            "age": be_indexer.NewIntValues(28),
            "city": be_indexer.NewStrValues("rural"),
            "is_vip": be_indexer.NewStrValues("false"),
        },
    }

    for i, profile := range userProfiles {
        result, _ := indexer.Retrieve(profile)
        fmt.Printf("用户%d匹配的广告: %v\n", i+1, result)
    }
}
```

### 示例4: 动态广告匹配

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 实时广告库
    ads := []*be_indexer.Document{
        be_indexer.NewDocument(2001).AddConjunction(
            be_indexer.NewConjunction().
                Include("gender", be_indexer.NewStrValues("female")).
                Include("interest", be_indexer.NewStrValues("beauty", "fashion")),
        ),
        be_indexer.NewDocument(2002).AddConjunction(
            be_indexer.NewConjunction().
                Include("gender", be_indexer.NewStrValues("male")).
                Include("interest", be_indexer.NewStrValues("tech", "gaming")),
        ),
    }

    builder.AddDocument(ads...)
    indexer := builder.BuildIndex()

    // 动态查询不同用户兴趣
    queries := []map[be_indexer.BEField]be_indexer.Values{
        {
            "gender": be_indexer.NewStrValues("female"),
            "interest": be_indexer.NewStrValues("beauty"),
        },
        {
            "gender": be_indexer.NewStrValues("male"),
            "interest": be_indexer.NewStrValues("gaming"),
        },
        {
            "gender": be_indexer.NewStrValues("female"),
            "interest": be_indexer.NewStrValues("tech"),
        },
    }

    for i, query := range queries {
        result, _ := indexer.Retrieve(query)
        fmt.Printf("查询%d匹配广告: %v\n", i+1, result)
    }
}
```

---

## 电商筛选系统

### 示例5: 商品筛选

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 商品库
    products := []*be_indexer.Document{
        // iPhone 15 Pro
        be_indexer.NewDocument(3001).AddConjunction(
            be_indexer.NewConjunction().
                Include("brand", be_indexer.NewStrValues("apple")).
                Include("category", be_indexer.NewStrValues("smartphone")).
                GreatThan("price", 8000).
                LessThan("price", 10000),
        ),
        // 小米手机
        be_indexer.NewDocument(3002).AddConjunction(
            be_indexer.NewConjunction().
                Include("brand", be_indexer.NewStrValues("xiaomi")).
                Include("category", be_indexer.NewStrValues("smartphone")).
                LessThan("price", 3000),
        ),
        // 华为笔记本
        be_indexer.NewDocument(3003).AddConjunction(
            be_indexer.NewConjunction().
                Include("brand", be_indexer.NewStrValues("huawei")).
                Include("category", be_indexer.NewStrValues("laptop")).
                Between("price", 5000, 8000),
        ),
    }

    builder.AddDocument(products...)
    indexer := builder.BuildIndex()

    // 筛选条件
    filters := map[be_indexer.BEField]be_indexer.Values{
        "category": be_indexer.NewStrValues("smartphone"),
        "price": be_indexer.NewIntValues(2500),
    }

    result, _ := indexer.Retrieve(filters)
    fmt.Println("筛选结果:", result)  // 匹配小米手机
}
```

### 示例6: 多条件组合筛选

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 更多商品数据
    products := []*be_indexer.Document{
        be_indexer.NewDocument(4001).AddConjunction(
            be_indexer.NewConjunction().
                Include("color", be_indexer.NewStrValues("red", "black")).
                Include("size", be_indexer.NewStrValues("M", "L")).
                Include("brand", be_indexer.NewStrValues("nike")),
        ),
        be_indexer.NewDocument(4002).AddConjunction(
            be_indexer.NewConjunction().
                Include("color", be_indexer.NewStrValues("blue")).
                Include("size", be_indexer.NewStrValues("S", "M")).
                Exclude("brand", be_indexer.NewStrValues("nike")),
        ),
    }

    builder.AddDocument(products...)
    indexer := builder.BuildIndex()

    // 查询1: 红色M码Nike
    query1 := map[be_indexer.BEField]be_indexer.Values{
        "color": be_indexer.NewStrValues("red"),
        "size": be_indexer.NewStrValues("M"),
        "brand": be_indexer.NewStrValues("nike"),
    }

    // 查询2: 蓝色M码非Nike
    query2 := map[be_indexer.BEField]be_indexer.Values{
        "color": be_indexer.NewStrValues("blue"),
        "size": be_indexer.NewStrValues("M"),
        "brand": be_indexer.NewStrValues("nike"),
    }

    result1, _ := indexer.Retrieve(query1)
    result2, _ := indexer.Retrieve(query2)

    fmt.Println("查询1结果:", result1)  // [4001]
    fmt.Println("查询2结果:", result2)  // [4002]
}
```

---

## 内容推荐系统

### 示例7: 文章推荐

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 文章库
    articles := []*be_indexer.Document{
        // AI文章
        be_indexer.NewDocument(5001).AddConjunction(
            be_indexer.NewConjunction().
                Include("tags", be_indexer.NewStrValues("ai", "machine-learning", "neural-network")),
        ),
        // 前端文章
        be_indexer.NewDocument(5002).AddConjunction(
            be_indexer.NewConjunction().
                Include("tags", be_indexer.NewStrValues("javascript", "react", "frontend")),
        ),
        // 后端文章
        be_indexer.NewDocument(5003).AddConjunction(
            be_indexer.NewConjunction().
                Include("tags", be_indexer.NewStrValues("golang", "backend", "api")),
        ),
    }

    builder.AddDocument(articles...)
    indexer := builder.BuildIndex()

    // 用户兴趣
    userInterests := map[be_indexer.BEField]be_indexer.Values{
        "tags": be_indexer.NewStrValues("ai", "golang"),
    }

    result, _ := indexer.Retrieve(userInterests)
    fmt.Println("推荐文章:", result)  // [5001, 5003]
}
```

### 示例8: 视频推荐

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 视频库
    videos := []*be_indexer.Document{
        be_indexer.NewDocument(6001).AddConjunction(
            be_indexer.NewConjunction().
                Include("category", be_indexer.NewStrValues("education")).
                Include("difficulty", be_indexer.NewStrValues("beginner")),
        ),
        be_indexer.NewDocument(6002).AddConjunction(
            be_indexer.NewConjunction().
                Include("category", be_indexer.NewStrValues("entertainment")).
                Include("duration", be_indexer.NewStrValues("short")),
        ),
        be_indexer.NewDocument(6003).AddConjunction(
            be_indexer.NewConjunction().
                Include("category", be_indexer.NewStrValues("education")).
                Include("difficulty", be_indexer.NewStrValues("advanced")).
                GreatThan("rating", 4.5),
        ),
    }

    builder.AddDocument(videos...)
    indexer := builder.BuildIndex()

    // 查找教育类视频
    query := map[be_indexer.BEField]be_indexer.Values{
        "category": be_indexer.NewStrValues("education"),
    }

    result, _ := indexer.Retrieve(query)
    fmt.Println("教育类视频:", result)  // [6001, 6003]
}
```

---

## 地理信息查询

### 示例9: GeoHash查询

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/parser"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 配置地理字段
    builder.ConfigField("location", be_indexer.FieldOption{
        Container: be_indexer.HolderNameDefault,
    })

    // 商店数据
    stores := []*be_indexer.Document{
        be_indexer.NewDocument(7001).AddConjunction(
            be_indexer.NewConjunction().
                Include("location", be_indexer.NewStrValues("wx4g0cpu")),
        ),
        be_indexer.NewDocument(7002).AddConjunction(
            be_indexer.NewConjunction().
                Include("location", be_indexer.NewStrValues("wx4g1qqq")),
        ),
    }

    builder.AddDocument(stores...)
    indexer := builder.BuildIndex()

    // 查询附近商店
    query := map[be_indexer.BEField]be_indexer.Values{
        "location": be_indexer.NewStrValues("wx4g0"),
    }

    result, _ := indexer.Retrieve(query)
    fmt.Println("附近商店:", result)
}
```

---

## roaringidx示例

### 示例10: 基础roaringidx使用

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/parser"
    "github.com/echoface/be_indexer/roaringidx"
)

func main() {
    // 创建roaringidx构建器
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

    // 添加文档
    doc1 := be_indexer.NewDocument(1)
    doc1.AddConjunction(be_indexer.NewConjunction().
        Include("age", be_indexer.NewIntValues(10, 20, 100)).
        Exclude("package", be_indexer.NewStrValues("com.example.bad")))

    builder.AddDocuments(doc1)

    // 构建索引器
    indexer, err := builder.BuildIndexer()
    if err != nil {
        panic(err)
    }

    // 创建扫描器
    scanner := roaringidx.NewScanner(indexer)

    // 检索
    assigns := map[be_indexer.BEField]be_indexer.Values{
        "age":     []int64{12, 20},
        "package": []interface{}{"com.example.good", "com.example.bad"},
    }

    docs, err := scanner.Retrieve(assigns)
    if err != nil {
        panic(err)
    }

    fmt.Println("匹配文档:", docs)

    // 查看原始结果
    fmt.Println("原始结果:", roaringidx.FormatBitMapResult(scanner.GetRawResult().ToArray()))

    scanner.Reset()
}
```

### 示例11: 大规模roaringidx

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

    // 配置多个字段
    fields := []string{"app", "version", "platform", "region"}
    for _, field := range fields {
        _ = builder.ConfigureField(field, roaringidx.FieldSetting{
            Container: roaringidx.ContainerNameDefault,
            Parser:    parser.NewHashStrParser(),
        })
    }

    // 批量添加大量文档
    for i := 0; i < 10000; i++ {
        doc := be_indexer.NewDocument(be_indexer.DocID(i))
        doc.AddConjunction(be_indexer.NewConjunction().
            Include("app", be_indexer.NewStrValues(fmt.Sprintf("app%d", i%100))).
            Include("version", be_indexer.NewStrValues(fmt.Sprintf("v%d", i%10))).
            Include("platform", be_indexer.NewStrValues(["android", "ios"][i%2])))
        builder.AddDocuments(doc)
    }

    indexer, _ := builder.BuildIndexer()
    scanner := roaringidx.NewScanner(indexer)

    // 检索
    query := map[be_indexer.BEField]be_indexer.Values{
        "app":      be_indexer.NewStrValues("app1"),
        "version":  be_indexer.NewStrValues("v1"),
        "platform": be_indexer.NewStrValues("android"),
    }

    result, _ := scanner.Retrieve(query)
    fmt.Printf("匹配文档数: %d\n", len(result))

    scanner.Reset()
}
```

---

## AC自动机示例

### 示例12: 关键词匹配

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 配置AC匹配器
    builder.ConfigField("keyword", be_indexer.FieldOption{
        Container: be_indexer.HolderNameACMatcher,
    })

    // 文章数据
    articles := []*be_indexer.Document{
        be_indexer.NewDocument(8001).AddConjunction(
            be_indexer.NewConjunction().
                Include("keyword", be_indexer.NewStrValues("advertisement", "promotion", "marketing")),
        ),
        be_indexer.NewDocument(8002).AddConjunction(
            be_indexer.NewConjunction().
                Include("keyword", be_indexer.NewStrValues("news", "report", "article")),
        ),
        be_indexer.NewDocument(8003).AddConjunction(
            be_indexer.NewConjunction().
                Include("keyword", be_indexer.NewStrValues("ad", "promo", "campaign")),
        ),
    }

    builder.AddDocument(articles...)
    indexer := builder.BuildIndex()

    // 查询：包含"ad"或"promo"的文章
    query := map[be_indexer.BEField]be_indexer.Values{
        "keyword": be_indexer.NewStrValues("ad", "promo"),
    }

    result, _ := indexer.Retrieve(query)
    fmt.Println("匹配文章:", result)  // [8001, 8003]
}
```

### 示例13: 敏感词过滤

```go
package main

import (
    "fmt"
    "github.com/echoface/be_indexer"
)

func main() {
    builder := be_indexer.NewIndexerBuilder()

    // 配置AC匹配器用于敏感词检测
    builder.ConfigField("sensitive_words", be_indexer.FieldOption{
        Container: be_indexer.HolderNameACMatcher,
    })

    // 敏感词规则
    sensitiveRules := []*be_indexer.Document{
        be_indexer.NewDocument(9001).AddConjunction(
            be_indexer.NewConjunction().
                Include("sensitive_words", be_indexer.NewStrValues("spam", "scam", "fraud")),
        ),
    }

    builder.AddDocument(sensitiveRules...)
    indexer := builder.BuildIndex()

    // 检测文本
    texts := []string{
        "This is a promotional ad",  // 包含"ad" -> 匹配
        "Check out this news report", // 不匹配
        "Amazing discount promo code", // 包含"promo" -> 匹配
    }

    for i, text := range texts {
        query := map[be_indexer.BEField]be_indexer.Values{
            "sensitive_words": be_indexer.NewStrValues(text),
        }

        result, _ := indexer.Retrieve(query)
        if len(result) > 0 {
            fmt.Printf("文本%d检测到敏感词\n", i+1)
        } else {
            fmt.Printf("文本%d通过检测\n", i+1)
        }
    }
}
```

---

## 自定义容器示例

### 示例14: 自定义容器

```go
package main

import (
    "fmt"
    "strings"
    "github.com/echoface/be_indexer"
)

// 自定义前缀匹配容器
type PrefixMatcherHolder struct {
    patterns map[string][]be_indexer.EntryID
}

func (h *PrefixMatcherHolder) Name() string {
    return "prefix_matcher"
}

func (h *PrefixMatcherHolder) CreateHolder(desc *be_indexer.FieldDesc) be_indexer.EntriesHolder {
    return &PrefixMatcherHolder{
        patterns: make(map[string][]be_indexer.EntryID),
    }
}

func (h *PrefixMatcherHolder) IndexingBETx(desc *be_indexer.FieldDesc, expr *be_indexer.BoolValues) (be_indexer.TxData, error) {
    // 实现索引逻辑
    return be_indexer.TxData{}, nil
}

func (h *PrefixMatcherHolder) DecodeTxData(data []byte) (be_indexer.TxData, error) {
    // 实现解码逻辑
    return be_indexer.TxData{}, nil
}

func (h *PrefixMatcherHolder) Query(prefix string) []be_indexer.EntryID {
    var results []be_indexer.EntryID
    for pattern, entries := range h.patterns {
        if strings.HasPrefix(pattern, prefix) {
            results = append(results, entries...)
        }
    }
    return results
}

func main() {
    // 注册自定义容器
    be_indexer.RegisterEntriesHolder("prefix_matcher", func() be_indexer.EntriesHolder {
        return &PrefixMatcherHolder{}
    })

    builder := be_indexer.NewIndexerBuilder()

    // 使用自定义容器
    builder.ConfigField("prefix", be_indexer.FieldOption{
        Container: "prefix_matcher",
    })

    // 构建文档
    doc := be_indexer.NewDocument(10001)
    doc.AddConjunction(
        be_indexer.NewConjunction().
            Include("prefix", be_indexer.NewStrValues("hello", "help")),
    )

    builder.AddDocument(doc)
    indexer := builder.BuildIndex()

    fmt.Println("自定义容器示例完成")
}
```

---

## 缓存使用示例

### 示例15: 内存缓存

```go
package main

import (
    "sync"
    "github.com/echoface/be_indexer"
    "github.com/echoface/be_indexer/codegen/cache"
    "google.golang.org/protobuf/proto"
)

type MemoryCache struct {
    mu   sync.RWMutex
    data map[be_indexer.ConjID][]byte
}

func (c *MemoryCache) Reset() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.data = make(map[be_indexer.ConjID][]byte)
}

func (c *MemoryCache) Get(conjID be_indexer.ConjID) ([]byte, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    data, ok := c.data[conjID]
    return data, ok
}

func (c *MemoryCache) Set(conjID be_indexer.ConjID, data []byte) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.data[conjID] = data
}

func main() {
    // 创建带缓存的构建器
    builder := be_indexer.NewIndexerBuilder(
        be_indexer.WithCacheProvider(&MemoryCache{
            data: make(map[be_indexer.ConjID][]byte),
        }),
    )

    // 构建重复的Conjunction（利用缓存）
    for i := 0; i < 1000; i++ {
        doc := be_indexer.NewDocument(be_indexer.DocID(i))
        doc.AddConjunction(
            be_indexer.NewConjunction().
                Include("category", be_indexer.NewStrValues("tech")).
                Include("tag", be_indexer.NewStrValues("popular")),
        )
        builder.AddDocument(doc)
    }

    indexer := builder.BuildIndex()
    fmt.Println("带缓存的索引构建完成")
}
```

---

## 性能测试示例

### 示例16: 性能基准测试

```go
package main

import (
    "fmt"
    "time"
    "github.com/echoface/be_indexer"
)

func BenchmarkIndexing(b *testing.B) {
    builder := be_indexer.NewIndexerBuilder()

    // 准备测试数据
    docs := make([]*be_indexer.Document, 10000)
    for i := 0; i < 10000; i++ {
        docs[i] = be_indexer.NewDocument(be_indexer.DocID(i))
        docs[i].AddConjunction(
            be_indexer.NewConjunction().
                Include("category", be_indexer.NewStrValues(fmt.Sprintf("cat%d", i%10))).
                GreatThan("score", int64(i%100)),
        )
    }

    start := time.Now()
    builder.AddDocument(docs...)
    indexer := builder.BuildIndex()
    elapsed := time.Since(start)

    fmt.Printf("索引构建时间: %v\n", elapsed)
    fmt.Printf("索引构建速度: %.2f docs/sec\n", float64(10000)/elapsed.Seconds())
}

func BenchmarkRetrieval(b *testing.B) {
    // 构建索引
    builder := be_indexer.NewIndexerBuilder()
    // ... 构建索引 ...

    query := map[be_indexer.BEField]be_indexer.Values{
        "category": be_indexer.NewStrValues("cat1"),
        "score": be_indexer.NewIntValues(50),
    }

    start := time.Now()
    for i := 0; i < 1000; i++ {
        _, _ = indexer.Retrieve(query)
    }
    elapsed := time.Since(start)

    fmt.Printf("检索时间: %v\n", elapsed)
    fmt.Printf("检索速度: %.2f queries/sec\n", float64(1000)/elapsed.Seconds())
}
```

---

## 总结

通过这些示例，您可以看到be_indexer在各种场景下的应用：

1. **广告投放**: 多维度用户特征匹配
2. **电商筛选**: 商品属性组合查询
3. **内容推荐**: 基于兴趣标签的推荐
4. **地理查询**: 基于位置的信息检索
5. **模式匹配**: AC自动机的字符串匹配
6. **大规模数据**: roaringidx的高性能解决方案

选择合适的实现和配置，可以满足从小型应用到大规模系统的各种需求。
