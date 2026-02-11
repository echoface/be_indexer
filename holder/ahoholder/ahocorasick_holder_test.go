package ahoholder

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"github.com/smartystreets/goconvey/convey"
	"sort"
	"testing"

	. "github.com/echoface/be_indexer"
)

func TestBEIndex_Retrieve6(t *testing.T) {
	LogLevel = DebugLevel
	builder := NewIndexerBuilder()
	builder.ConfigField("keyword", core.FieldOption{
		Container: core.HolderNameACMatcher,
	})

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := core.NewDocument(12)
	conj := core.NewConjunction().
		In("tag", core.NewInt32Values(1)).
		In("keyword", core.NewStrValues("abc", "红包", "棋牌"))
	doc.AddConjunction(conj)
	_ = builder.AddDocument(doc)

	// 13: (tag IN 1 && age Not 27) or (tag Not 60)
	doc = core.NewDocument(13)
	conj = core.NewConjunction().
		In("tag", core.NewInt32Values(1)).NotIn("age", core.NewInt32Values(27, 15, 18, 22, 28, 32))
	doc.AddConjunction(conj)
	_ = builder.AddDocument(doc)

	// 14: (tag in 1,2 && tag in 12) or ("age In 60") or (sex In man)
	doc = core.NewDocument(14)
	conj = core.NewConjunction().
		In("tag", core.NewInt32Values(1, 2)).
		In("sex", core.NewStrValues("women"))
	conj3 := core.NewConjunction().
		NotIn("keyword", core.NewStrValues("红包", "拉拉", "解放")).
		In("age", core.NewIntValues(12, 24, 28))
	doc.AddConjunction(conj, conj3)
	_ = builder.AddDocument(doc)

	convey.Convey("test ac matcher retrieve", t, func() {

		indexer, err := builder.BuildIndex()
		convey.So(err, convey.ShouldBeNil)
		PrintIndexInfo(indexer)

		var ids core.DocIDList
		ids, err = indexer.Retrieve(core.Assignments{
			"sex":     []string{"man"},
			"keyword": core.NewStrValues("解放军发红包", "abc英文歌"),
			"age":     []int{28, 2, 27},
			"tag":     []int{1, 2, 27},
		}, WithStepDetail())
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, core.DocIDList{12})
		convey.So(err, convey.ShouldBeNil)
	})
}
