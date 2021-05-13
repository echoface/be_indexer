# Boolean Expression Index

算法描述来源于论文：[Boolean expression index](https://theory.stanford.edu/~sergei/papers/vldb09-indexing.pdf)
为什么写它:
- 大佬写的(优秀的代码)并没有开源
- 网络上能看到的描述和实现模糊不清，完全不能够工程化
- 在线广告、某些功能模块借助其可以实现非常优雅的代码实现
- 论文没有提及的多值查询的问题没有直接给出实现提示，但是实际应用中都特别需要支持

这个Golang版本基本上是之前实现的C++版本的clone,[C++实现移步](https://github.com/echoface/ltio/blob/master/components/boolean_indexer)

## usage:

```go
func main() {
    builder := IndexerBuilder{
        Documents: make(map[int32]*Document),
    }

    for _, doc := range buildTestDoc() {
        builder.AddDocument(doc)
    }

    indexer := builder.BuildIndex()

    //fmt.Println(indexer.DumpSizeEntries())

    result, e := indexer.Retrieve(map[BEField]Values{
        "age": NewValues2(5),
    })
    fmt.Println(e, result)

    result, e = indexer.Retrieve(map[BEField]Values{
        "ip": NewStrValues2("localhost"),
    })
    fmt.Println(e, result)

    result, e = indexer.Retrieve(map[BEField]Values{
        "age":  NewIntValues2(1),
        "city": NewStrValues2("sh"),
        "tag":  NewValues2("tag1"),
    })
    fmt.Println(e, result)
}
```

使用限制：
- 文档ID最大值限制为:`2^32`
- 每个文档最多拥有256个Conjunction
- 每个DNF最大支持组合条件(field)个数：256
- 支持任何可以通过parse值化的类型，见parser的定义
- 最大值数量限制：数值/字符串各2^56个（约7.205...e16）

# Copyright and License

Copyright (C) 2018, by HuanGong [gonghuan.dev@gmail.com](mailto:gonghuan.dev@gmail.com).

Under the Apache License, Version 2.0.

See the [LICENSE](LICENSE) file for details.
