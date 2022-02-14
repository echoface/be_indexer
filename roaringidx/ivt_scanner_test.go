package roaringidx

import (
	"fmt"
	"testing"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/smartystreets/goconvey/convey"
)

func TestIvtScanner_Retrieve(t *testing.T) {

	convey.Convey("test configure Indexer", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		_ = builder.ConfigureField("ad_id", FieldSetting{
			Container: "default",
			Parser:    parser.NewNumberParser(),
		})
		_ = builder.ConfigureField("package", FieldSetting{
			Container: "default",
			Parser:    parser.NewStrHashParser(),
		})

		doc1 := be_indexer.NewDocument(1)
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("package", be_indexer.NewStrValues("com.echoface.x")))

		doc2 := be_indexer.NewDocument(5)
		doc2.AddConjunction(be_indexer.NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc2.AddConjunction(be_indexer.NewConjunction().
			Exclude("package", be_indexer.NewStrValues("com.echoface.not")))

		doc3 := be_indexer.NewDocument(20)
		doc3.AddConjunctions(be_indexer.NewConjunction())

		doc4 := be_indexer.NewDocument(50)
		doc4.AddConjunction(be_indexer.NewConjunction().
			Exclude("ad_id", be_indexer.NewIntValues(100, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))

		builder.AddDocuments(doc1, doc2, doc3, doc4)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		scanner := NewScanner(indexer)
		docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"ad_id":   []interface{}{100, 102},
			"package": []interface{}{"com.echoface.be", "com.echoface.not"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{1, 5, 20})

		scanner.Reset()
		docs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"com.echoface.not"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{20})
	})
}

func TestIvtScanner_Retrieve2(t *testing.T) {

	convey.Convey("test ac logic retrieve", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		_ = builder.ConfigureField("keywords", FieldSetting{
			Container: "ac_matcher",
		})

		doc1 := be_indexer.NewDocument(1)
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("keywords", be_indexer.NewStrValues("红包", "色情")))

		doc2 := be_indexer.NewDocument(5)
		doc2.AddConjunction(be_indexer.NewConjunction().
			Include("keywords", be_indexer.NewStrValues("红包", "舒淇")))

		doc3 := be_indexer.NewDocument(10)
		doc3.AddConjunctions(be_indexer.NewConjunction())

		doc4 := be_indexer.NewDocument(20)
		doc4.AddConjunction(be_indexer.NewConjunction().
			Exclude("keywords", be_indexer.NewStrValues("色情", "在线视频")))

		_ = builder.AddDocuments(doc1, doc2, doc3, doc4)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		scanner := NewScanner(indexer)
		docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"keywords": []interface{}{"恭喜发财红包拿来", "坚决查处色情娱乐场所"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{1, 5, 10})
		fmt.Println("result:", FormatBitMapResult(scanner.GetRawResult().ToArray()))

		scanner.Reset()
		docs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"恭喜发财红包拿来"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{10, 20})
		fmt.Println("result:", FormatBitMapResult(scanner.GetRawResult().ToArray()))

		scanner.Reset()
		docs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"坚决查处色情娱乐场所"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{10, 20})
		fmt.Println("result:", FormatBitMapResult(scanner.GetRawResult().ToArray()))
	})
}

func TestIvtScanner_Retrieve3(t *testing.T) {

	convey.Convey("test conjunction duplicated", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		_ = builder.ConfigureField("ad_id", FieldSetting{
			Container: "default",
			Parser:    parser.NewNumberParser(),
		})
		_ = builder.ConfigureField("package", FieldSetting{
			Container: "default",
			Parser:    parser.NewStrHashParser(),
		})

		doc1 := be_indexer.NewDocument(1)
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("package", be_indexer.NewStrValues("com.echoface.x")))

		_ = builder.AddDocuments(doc1)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		scanner := NewScanner(indexer)
		docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"ad_id":   []interface{}{100, 102},
			"package": []interface{}{"com.echoface.be", "com.echoface.x"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{1})
		fmt.Println("result:", FormatBitMapResult(scanner.GetRawResult().ToArray()))
		convey.So(scanner.GetRawResult().GetCardinality(), convey.ShouldEqual, 2)
	})
}
func TestIvtScanner_Retrieve4(t *testing.T) {

	convey.Convey("test retrieve with hint doc", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		_ = builder.ConfigureField("ad_id", FieldSetting{
			Container: "default",
			Parser:    parser.NewNumberParser(),
		})
		_ = builder.ConfigureField("package", FieldSetting{
			Container: "default",
			Parser:    parser.NewStrHashParser(),
		})

		doc1 := be_indexer.NewDocument(1)
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(be_indexer.NewConjunction().
			Include("package", be_indexer.NewStrValues("com.echoface.x")))
		_ = builder.AddDocument(doc1)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)

		scanner := NewScanner(indexer)
		docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"ad_id":   []interface{}{100, 102},
			"package": []interface{}{"com.echoface.be", "com.echoface.x"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{1})
		convey.So(scanner.GetRawResult().GetCardinality(), convey.ShouldEqual, 2)

		scanner.Reset()
		scanner.SetDebug(true)
		scanner.WithHint(1, 2, 3)
		docs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"ad_id":   []interface{}{100, 102},
			"package": []interface{}{"com.echoface.be", "com.echoface.x"},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(docs, convey.ShouldResemble, []uint64{1})
		convey.So(scanner.GetRawResult().GetCardinality(), convey.ShouldEqual, 2)
	})
}
