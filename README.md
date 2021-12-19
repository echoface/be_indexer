# Boolean Expression Index

## Boolean expression index
算法描述来源于论文：[Boolean expression index](https://theory.stanford.edu/~sergei/papers/vldb09-indexing.pdf)
为什么写它:
- 大佬写的(优秀的代码)并没有开源
- 网络上能看到的描述和实现模糊不清，完全不能够工程化
- 在线广告、某些功能模块借助其可以实现非常优雅的代码实现
- 论文没有提及的多值查询的问题没有直接给出实现提示，但是实际应用中都特别需要支持

这个Golang版本基本上是之前实现的C++版本的clone,[C++实现移步](https://github.com/echoface/ltio/blob/master/components/boolean_indexer)
使用限制：
因为存在对信息存在编码和压缩，所以存在一些限制，使用时注意规避
- 文档ID最大值限制为:`2^32`
- 每个文档最多拥有256个Conjunction
- 每个DNF最大支持组合条件(field)个数：256
- 支持任何可以通过parse值化的类型，见parser的定义
- 默认倒排容器是hash, 因抽用了8bit用在存储field id，所以最大值数量限制：数值/字符串各2^56个（约7.205...e16）

### usage:

```go
package main

import (
	"fmt"

	"github.com/echoface/be_indexer/parser"

	"github.com/echoface/be_indexer/util"

	"github.com/echoface/be_indexer"
)

func buildTestDoc() []*be_indexer.Document {
	return []*be_indexer.Document{}
}

func main() {
	builder := be_indexer.NewIndexerBuilder()
	// or use a compacted version, it faster about 12% than default
	// builder := be_indexer.NewCompactIndexerBuilder()

	// optional special a holder/container for field
	builder.ConfigField("keyword", be_indexer.FieldOption{
		Parser:    parser.ParserNameCommon,
		Container: be_indexer.HolderNameACMatcher,
	})

	for _, doc := range buildTestDoc() {
		err := builder.AddDocument(doc)
		util.PanicIfErr(err, "document can't be resolved")
	}

	indexer := builder.BuildIndex()

	result, e := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age": be_indexer.NewValues2(5),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"ip": be_indexer.NewStrValues2("localhost"),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewIntValues2(1),
		"city": be_indexer.NewStrValues2("sh"),
		"tag":  be_indexer.NewValues2("tag1"),
	}, be_indexer.WithStepDetail(), be_indexer.WithDumpEntries())
	fmt.Println(e, result)
}
```


## roaring bitmap based boolean expression index(roaringidx)
基于bitmap的布尔索引实现，相对于Boolean expression index论文的实现， 是利用bitmap在集合运算方面的优势实现的DNF Match逻辑，目前支持普通的倒排
以及基于AhoCorasick的字符串模式匹配逻辑实现。从benchmark 结果来看， 在大规模多fields的场景下， 性能相对于Boolean expression index的实现性能
相对来说要差一些，但是其可理解性要好一点。 bitmap方面借助roaring bitmap的实现，在大基数稀疏场景下可以节省大量的内存。aho corasick 选型上也选取
了使用double array trie的实现，索引上内存有所压缩。

### usage
```go
package main

import (
	"fmt"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
	"github.com/echoface/be_indexer/util"
)

func main() {

	builder := roaringidx.NewIndexerBuilder()

	builder.ConfigureField("ad_id", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameDefault,
		Parser:    parser.ParserNameNumber,
	})
	builder.ConfigureField("package", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameDefault,
		Parser:    parser.ParserNameStrHash,
	})
	builder.ConfigureField("keywords", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameAcMatch,
		Parser:    parser.ParserNameCommon,
	})

	doc1 := be_indexer.NewDocument(1)
	doc1.AddConjunction(be_indexer.NewConjunction().
		Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
		Exclude("package", be_indexer.NewStrValues("com.echoface.not")))
	doc1.AddConjunction(be_indexer.NewConjunction().
		Include("package", be_indexer.NewStrValues("com.echoface.in")))

	doc3 := be_indexer.NewDocument(20)
	doc3.AddConjunctions(be_indexer.NewConjunction())

	doc4 := be_indexer.NewDocument(50)
	doc4.AddConjunction(be_indexer.NewConjunction().
		Exclude("ad_id", be_indexer.NewIntValues(100, 108)).
		Include("package", be_indexer.NewStrValues("com.echoface.be")))

	builder.AddDocuments(doc1, doc3, doc4)

	indexer, err := builder.BuildIndexer()
	util.PanicIfErr(err, "should not err here")

	scanner := roaringidx.NewScanner(indexer)
	conjIDs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"ad_id":   []interface{}{100, 102},
		"package": []interface{}{"com.echoface.be", "com.echoface.not"},
	})
	fmt.Println(roaringidx.FormatBitMapResult(conjIDs))
	scanner.Reset()
}
```


## Copyright and License

Copyright (C) 2018, by HuanGong [gonghuan.dev@gmail.com](mailto:gonghuan.dev@gmail.com).

Under the Apache License, Version 2.0.

See the [LICENSE](LICENSE) file for details.
