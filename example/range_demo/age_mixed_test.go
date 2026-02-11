package range_demo

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"sort"
	"testing"

	"github.com/echoface/be_indexer"
	_ "github.com/echoface/be_indexer/holder/rangeholder" // 注册 optimized_range
	"github.com/smartystreets/goconvey/convey"
)

// 演示：一个 "age" 字段混合了 >, Between, In 三种逻辑
// 结论：必须使用 "optimized_range" Holder
func TestAgeMixedQueries(t *testing.T) {
	convey.Convey("演示：Age字段混合范围和离散值查询", t, func() {
		// 1. 初始化 Builder
		builder := be_indexer.NewCompactIndexerBuilder()
		
		// 关键配置：指定 age 字段使用 optimized_range
		// 只有这个 Holder 能同时高效处理 Range 和 In
		builder.ConfigField("age", core.FieldOption{
			Container: "optimized_range",
		})

		// 2. 构造文档 (模拟用户画像/广告定位)
		
		// Doc1: 针对成年人 (age >= 18)
		// 逻辑转换: >= 18 等价于 > 17
		doc1 := core.NewDocument(1)
		doc1.AddConjunction(core.NewConjunction().
			GreaterThan("age", 17))

		// Doc2: 针对青年群体 (age between 20 and 30)
		// 默认是左闭右开 [20, 30)
		doc2 := core.NewDocument(2)
		doc2.AddConjunction(core.NewConjunction().
			Between("age", 20, 30))

		// Doc3: 针对特定年龄点 (age in [18, 25, 60])
		// 比如特定生日活动
		doc3 := core.NewDocument(3)
		doc3.AddConjunction(core.NewConjunction().
			In("age", []int{18, 25, 60}))

		err := builder.AddDocument(doc1, doc2, doc3)
		convey.So(err, convey.ShouldBeNil)

		// 3. 构建索引
		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)

		// 4. 验证查询 (Query)
		// 模拟一个用户进来，携带他的 age 属性

		// Case A: 用户 age = 18
		// 预期命中: 
		// - Doc1 (>= 18) -> 命中
		// - Doc2 [20, 30) -> 不命中
		// - Doc3 {18, 25, 60} -> 命中
		ids, _ := indexer.Retrieve(core.Assignments{"age": 18})
		sort.Sort(ids)
		fmt.Printf("Query age=18, Matched Docs: %v\n", ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{1, 3})

		// Case B: 用户 age = 25
		// 预期命中:
		// - Doc1 (>= 18) -> 命中
		// - Doc2 [20, 30) -> 命中
		// - Doc3 {18, 25, 60} -> 命中
		ids, _ = indexer.Retrieve(core.Assignments{"age": 25})
		sort.Sort(ids)
		fmt.Printf("Query age=25, Matched Docs: %v\n", ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{1, 2, 3})

		// Case C: 用户 age = 60
		// 预期命中:
		// - Doc1 (>= 18) -> 命中
		// - Doc2 [20, 30) -> 不命中
		// - Doc3 {18, 25, 60} -> 命中
		ids, _ = indexer.Retrieve(core.Assignments{"age": 60})
		sort.Sort(ids)
		fmt.Printf("Query age=60, Matched Docs: %v\n", ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{1, 3})

		// Case D: 用户 age = 10
		// 预期命中: 无
		ids, _ = indexer.Retrieve(core.Assignments{"age": 10})
		fmt.Printf("Query age=10, Matched Docs: %v\n", ids)
		convey.So(len(ids), convey.ShouldEqual, 0)
	})
}
