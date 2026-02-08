package be_indexer

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

type TestUserTargeting struct {
	ID   int64   `be_indexer:"doc_id"`
	Age  []int   `be_indexer:"age,include"`
	City string  `be_indexer:"city,include"`
	Tags []int   `be_indexer:"tags,exclude"`
}

func TestDocumentBuilder_Build(t *testing.T) {
	convey.Convey("test DocumentBuilder basic", t, func() {
		builder := NewDocumentBuilder()

		targeting := TestUserTargeting{
			ID:   100,
			Age:  []int{18, 25, 30},
			City: "beijing",
			Tags: []int{1, 2, 3},
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc, convey.ShouldNotBeNil)
		convey.So(doc.ID, convey.ShouldEqual, 100)
		// 所有字段在一个 Conjunction 中（AND 关系）
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		conj := doc.Cons[0]
		convey.So(len(conj.Expressions), convey.ShouldEqual, 3)

		// 检查 age IN
		ageExprs := conj.Expressions["age"]
		convey.So(len(ageExprs), convey.ShouldEqual, 1)
		convey.So(ageExprs[0].Incl, convey.ShouldBeTrue)

		// 检查 city IN
		cityExprs := conj.Expressions["city"]
		convey.So(len(cityExprs), convey.ShouldEqual, 1)
		convey.So(cityExprs[0].Incl, convey.ShouldBeTrue)

		// 检查 tags NOT IN
		tagsExprs := conj.Expressions["tags"]
		convey.So(len(tagsExprs), convey.ShouldEqual, 1)
		convey.So(tagsExprs[0].Incl, convey.ShouldBeFalse)
	})
}

func TestDocumentBuilder_BuildSlice(t *testing.T) {
	convey.Convey("test DocumentBuilder BuildSlice", t, func() {
		builder := NewDocumentBuilder()

		targetings := []TestUserTargeting{
			{ID: 1, Age: []int{18}, City: "beijing", Tags: []int{1}},
			{ID: 2, Age: []int{20}, City: "shanghai", Tags: []int{2}},
			{ID: 3, Age: []int{25}, City: "guangzhou", Tags: []int{3}},
		}

		docs, err := builder.BuildSlice(targetings)
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(docs), convey.ShouldEqual, 3)

		for i, doc := range docs {
			convey.So(doc.ID, convey.ShouldEqual, DocID(i+1))
		}
	})
}

func TestDocumentBuilder_NestedStruct(t *testing.T) {
	convey.Convey("test DocumentBuilder nested struct", t, func() {
		builder := NewDocumentBuilder()

		// 嵌套 struct 用于生成 OR 关系的 Conjunction
		type Location struct {
			Province string `be_indexer:"province"`
			City     string `be_indexer:"city"`
		}

		type MultiRegionTargeting struct {
			ID     int64      `be_indexer:"doc_id"`
			Region []Location `be_indexer:"region"` // 多个地区，生成多个 Conjunction
		}

		targeting := MultiRegionTargeting{
			ID: 200,
			Region: []Location{
				{Province: "beijing", City: "chaoyang"},
				{Province: "shanghai", City: "pudong"},
			},
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc, convey.ShouldNotBeNil)
		// 主 Conjunction（空，因为 Region 在单独的 Conjunction 中）
		// + 2 个 Location 生成的 Conjunction
		convey.So(len(doc.Cons), convey.ShouldEqual, 2) // 2 个 OR 关系的 Conjunction

		// 第一个 Conjunction (beijing, chaoyang)
		conj1 := doc.Cons[0]
		convey.So(len(conj1.Expressions), convey.ShouldEqual, 2)
		convey.So(conj1.Expressions["province"], convey.ShouldNotBeNil)
		convey.So(conj1.Expressions["city"], convey.ShouldNotBeNil)

		// 第二个 Conjunction (shanghai, pudong)
		conj2 := doc.Cons[1]
		convey.So(len(conj2.Expressions), convey.ShouldEqual, 2)
	})

}

func TestDocumentBuilder_EmptyValues(t *testing.T) {
	convey.Convey("test DocumentBuilder with empty values", t, func() {
		builder := NewDocumentBuilder()

		type EmptyTargeting struct {
			ID  int64 `be_indexer:"doc_id"`
			Age []int `be_indexer:"age,include"`
		}

		targeting := EmptyTargeting{
			ID:  300,
			Age: []int{},
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc, convey.ShouldNotBeNil)
		// 空 slice 不生成表达式，也没有其他字段，所以没有 Conjunction
		convey.So(len(doc.Cons), convey.ShouldEqual, 0)
	})
}

func TestDocumentBuilder_MustBuild(t *testing.T) {
	convey.Convey("test DocumentBuilder MustBuild panic", t, func() {
		builder := NewDocumentBuilder()

		type NoDocID struct {
			Data int `be_indexer:"data"`
		}

		convey.So(func() {
			builder.MustBuild(NoDocID{Data: 1})
		}, convey.ShouldPanic)
	})
}

func TestDocumentBuilder_ExprTypes(t *testing.T) {
	convey.Convey("test DocumentBuilder expression types", t, func() {
		builder := NewDocumentBuilder()

		type ExprTargeting struct {
			ID      int64 `be_indexer:"doc_id"`
			AgeMin  int64 `be_indexer:"age,include,gt"`
			AgeMax  int64 `be_indexer:"age,include,lt"`
			Score   int64 `be_indexer:"score"`
		}

		targeting := ExprTargeting{
			ID:     400,
			AgeMin: 18,
			AgeMax: 60,
			Score:  100,
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc, convey.ShouldNotBeNil)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		conj := doc.Cons[0]
		// age 有两个表达式（gt 和 lt），score 有一个
		convey.So(len(conj.Expressions), convey.ShouldEqual, 2)
	})
}

func TestDocumentBuilder_CustomDocIDField(t *testing.T) {
	convey.Convey("test DocumentBuilder custom doc ID field", t, func() {
		builder := NewDocumentBuilder().SetDocIDField("DocumentID")

		type CustomDocID struct {
			DocumentID int64 `be_indexer:"doc_id"`
			Name      int   `be_indexer:"name"`
		}

		targeting := CustomDocID{
			DocumentID: 500,
			Name:       1,
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc.ID, convey.ShouldEqual, 500)
	})
}

func TestDocumentBuilder_DefaultIDField(t *testing.T) {
	convey.Convey("test DocumentBuilder default id field", t, func() {
		builder := NewDocumentBuilder()

		// 使用默认的 "id" 字段名作为 DocID
		type DefaultID struct {
			ID   int64 `be_indexer:"doc_id"`
			Data int   `be_indexer:"data"`
		}

		targeting := DefaultID{
			ID:   600,
			Data: 1,
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc.ID, convey.ShouldEqual, 600)
	})
}

func TestDocumentBuilder_NonSliceNested(t *testing.T) {
	convey.Convey("test DocumentBuilder non-slice nested struct", t, func() {
		builder := NewDocumentBuilder()

		// 嵌套非 slice struct
		type Contact struct {
			Phone string `be_indexer:"phone"`
			Email string `be_indexer:"email"`
		}

		type Person struct {
			ID      int64   `be_indexer:"doc_id"`
			Name    string  `be_indexer:"name"`
			Contact Contact // 非 slice，展开到主 Conjunction
		}

		targeting := Person{
			ID:   700,
			Name: "John",
			Contact: Contact{
				Phone: "123456",
				Email: "john@example.com",
			},
		}

		doc, err := builder.Build(targeting)
		convey.So(err, convey.ShouldBeNil)
		convey.So(doc, convey.ShouldNotBeNil)
		convey.So(len(doc.Cons), convey.ShouldEqual, 1)

		conj := doc.Cons[0]
		// 所有字段在一个 Conjunction 中
		convey.So(len(conj.Expressions), convey.ShouldEqual, 3)
		convey.So(conj.Expressions["name"], convey.ShouldNotBeNil)
		convey.So(conj.Expressions["phone"], convey.ShouldNotBeNil)
		convey.So(conj.Expressions["email"], convey.ShouldNotBeNil)
	})
}
