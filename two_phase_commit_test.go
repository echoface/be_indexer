package be_indexer

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestTwoPhaseCommit_DataConsistency(t *testing.T) {
	convey.Convey("test two-phase commit ensures data consistency", t, func() {
		builder := NewIndexerBuilder()
		builder.ConfigField("age", FieldOption{Container: HolderNameDefault})
		builder.ConfigField("city", FieldOption{Container: HolderNameDefault})

		// Document 有 3 个 Conjunction
		doc := NewDocument(1)
		doc.AddConjunction(
			// Conj 0: 正常
			NewConjunction().In("age", []int{18, 25}).In("city", []string{"beijing"}),
		)
		doc.AddConjunction(
			// Conj 1: 正常
			NewConjunction().In("age", []int{30}).In("city", []string{"shanghai"}),
		)
		doc.AddConjunction(
			// Conj 2: 正常
			NewConjunction().In("age", []int{35}).In("city", []string{"guangzhou"}),
		)

		err := builder.AddDocument(doc)
		convey.So(err, convey.ShouldBeNil)

		index := builder.BuildIndex()

		// 验证所有 Conjunction 都成功索引
		// age=18, city=beijing 应该匹配 doc 1
		result1, err := index.Retrieve(Assignments{
			"age":  []int{18},
			"city": []string{"beijing"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(result1, convey.ShouldContain, DocID(1))

		// age=30, city=shanghai 应该匹配 doc 1
		result2, err := index.Retrieve(Assignments{
			"age":  []int{30},
			"city": []string{"shanghai"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(result2, convey.ShouldContain, DocID(1))

		// age=35, city=guangzhou 应该匹配 doc 1
		result3, err := index.Retrieve(Assignments{
			"age":  []int{35},
			"city": []string{"guangzhou"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(result3, convey.ShouldContain, DocID(1))
	})
}

func TestTwoPhaseCommit_Atomicity(t *testing.T) {
	convey.Convey("test two-phase commit atomicity - skip bad conjunction", t, func() {
		// 使用 SkipBadConj 行为
		builder := NewIndexerBuilder(WithBadConjBehavior(SkipBadConj))
		builder.ConfigField("age", FieldOption{Container: HolderNameDefault})
		builder.ConfigField("city", FieldOption{Container: HolderNameDefault})

		// Document 有 2 个 Conjunction
		doc := NewDocument(1)
		doc.AddConjunction(
			// Conj 0: 正常
			NewConjunction().In("age", []int{18}).In("city", []string{"beijing"}),
		)
		doc.AddConjunction(
			// Conj 1: 无效的表达式（city 字段传入数字，解析会失败）
			// 但使用 SkipBadConj，应该跳过这个 Conjunction 继续处理
			NewConjunction().In("age", []int{25}).In("city", 12345), // 12345 不是有效的城市值
		)

		err := builder.AddDocument(doc)
		// SkipBadConj 模式下，错误被记录但不会返回错误
		convey.So(err, convey.ShouldBeNil)

		index := builder.BuildIndex()

		// Conj 0 应该成功索引（因为 Conj 1 被跳过，不影响 Conj 0）
		result, err := index.Retrieve(Assignments{
			"age":  []int{18},
			"city": []string{"beijing"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(result, convey.ShouldContain, DocID(1))
	})
}
