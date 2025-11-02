# how to customize parser/tokenizer

通常而言parser/tokenizer是由索引数据的容器决定的，例如: 对于模式匹配查询索引的容器`ac_matcher`就决定
了布尔逻辑表达的描述是字符串，索引查询时的assign同样也要求字符串;
```
doc 1:  keywords in ["红包", "av", "adult"]

indexing query input: "发红包来领取adult av观看福利"
indexing query output: [doc1]
```

本库中提供了内置的几种简单的容器，其中默认的容器支持指定字段的parser; 可以通过复写注册factory creator
来实现, example/unittest 中均提供了相关的示例，这里文档同样做个说明：

```
type (
	DefaultEntriesHolder struct {
        ....

		Parser      parser.FieldValueParser
		FieldParser map[BEField]parser.FieldValueParser
	}
)

// factory bulder
func init() {
	RegisterEntriesHolder(HolderNameDefault, func() EntriesHolder {
		return NewDefaultEntriesHolder()
	})
}
```

默认注册的构造Builder只有一个默认的common:`parser/common_parser.go`解析器，
如果某些field需要特殊的解析逻辑，可以通过如下的方式重新覆盖来支持诸如:`age in ["18:30"]`的表达,
```
	be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.FieldParser = map[be_indexer.BEField]parser.FieldValueParser{
			"age": parser.NewNumRangeParser(),
		}
		return holder
	})
```
