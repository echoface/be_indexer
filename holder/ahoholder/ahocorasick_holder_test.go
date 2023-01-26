package ahoholder

import (
	"fmt"
	"github.com/smartystreets/goconvey/convey"
	"sort"
	"testing"

	. "github.com/echoface/be_indexer"
)

func TestBEIndex_Retrieve6(t *testing.T) {
	LogLevel = DebugLevel
	builder := NewIndexerBuilder()
	builder.ConfigField("keyword", FieldOption{
		Container: HolderNameACMatcher,
	})

	// 12: (tag IN 1 && age In 27,50) or (tag IN 12)
	doc := NewDocument(12)
	conj := NewConjunction().
		In("tag", NewInt32Values(1)).
		In("keyword", NewStrValues("abc", "红包", "棋牌"))
	doc.AddConjunction(conj)
	_ = builder.AddDocument(doc)

	// 13: (tag IN 1 && age Not 27) or (tag Not 60)
	doc = NewDocument(13)
	conj = NewConjunction().
		In("tag", NewInt32Values(1)).NotIn("age", NewInt32Values(27, 15, 18, 22, 28, 32))
	doc.AddConjunction(conj)
	_ = builder.AddDocument(doc)

	// 14: (tag in 1,2 && tag in 12) or ("age In 60") or (sex In man)
	doc = NewDocument(14)
	conj = NewConjunction().
		In("tag", NewInt32Values(1, 2)).
		In("sex", NewStrValues("women"))
	conj3 := NewConjunction().
		NotIn("keyword", NewStrValues("红包", "拉拉", "解放")).
		In("age", NewIntValues(12, 24, 28))
	doc.AddConjunction(conj, conj3)
	_ = builder.AddDocument(doc)

	convey.Convey("test ac matcher retrieve", t, func() {

		indexer := builder.BuildIndex()
		PrintIndexInfo(indexer)
		PrintIndexEntries(indexer)

		var err error
		var ids DocIDList
		ids, err = indexer.Retrieve(Assignments{
			"sex":     []string{"man"},
			"keyword": NewStrValues("解放军发红包", "abc英文歌"),
			"age":     []int{28, 2, 27},
			"tag":     []int{1, 2, 27},
		}, WithDumpEntries(), WithStepDetail())
		fmt.Println(ids)
		sort.Sort(ids)
		convey.So(ids, convey.ShouldResemble, DocIDList{12})
		convey.So(err, convey.ShouldBeNil)
	})
}
