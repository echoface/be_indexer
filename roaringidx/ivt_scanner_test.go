package roaringidx

import (
	"fmt"
	"github.com/echoface/be_indexer"
	parser "github.com/echoface/be_indexer/parser/v2"
	"github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestIvtScanner_Retrieve(t *testing.T) {

	convey.Convey("test configure Indexer", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		builder.ConfigureField("ad_id", FieldSetting{
			Container: "default",
			Parser:    parser.ParserNameNumber,
		})
		builder.ConfigureField("package", FieldSetting{
			Container: "default",
			Parser:    parser.ParserNameStrHash,
		})

		doc1 := NewDocument(1)
		doc1.AddConjunction(NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(NewConjunction().
			Include("package", be_indexer.NewStrValues("com.echoface.x")))

		doc2 := NewDocument(5)
		doc2.AddConjunction(NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc2.AddConjunction(NewConjunction().
			Exclude("package", be_indexer.NewStrValues("com.echoface.not")))

		doc3 := NewDocument(20)
		doc3.AddConjunctions(NewConjunction())

		doc4 := NewDocument(50)
		doc4.AddConjunction(NewConjunction().
			Exclude("ad_id", be_indexer.NewIntValues(100, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))

		builder.AddDocuments(doc1, doc2, doc3, doc4)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		scanner := NewScanner(indexer)
		conjs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"ad_id":   []interface{}{100, 102},
			"package": []interface{}{"com.echoface.be", "com.echoface.not"},
		})
		convey.So(err, convey.ShouldBeNil)
		fmt.Println(FormatBitMapResult(conjs))

		scanner.Reset()
		conjs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"com.echoface.not"},
		})
		convey.So(err, convey.ShouldBeNil)
		fmt.Println(FormatBitMapResult(conjs))
	})
}

func TestIvtScanner_Retrieve2(t *testing.T) {

	convey.Convey("test ac logic retrieve", t, func() {
		builder := NewIndexerBuilder()
		convey.So(builder, convey.ShouldNotBeNil)

		builder.ConfigureField("keywords", FieldSetting{
			Container: "ac_matcher",
			Parser:    "",
		})

		doc1 := NewDocument(1)
		doc1.AddConjunction(NewConjunction().
			Include("keywords", be_indexer.NewStrValues("红包", "色情")))

		doc2 := NewDocument(5)
		doc2.AddConjunction(NewConjunction().
			Include("keywords", be_indexer.NewStrValues("红包", "舒淇")))

		doc3 := NewDocument(10)
		doc3.AddConjunctions(NewConjunction())

		doc4 := NewDocument(20)
		doc4.AddConjunction(NewConjunction().
			Exclude("keywords", be_indexer.NewStrValues("色情", "在线视频")))

		builder.AddDocuments(doc1, doc2, doc3, doc4)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)

		scanner := NewScanner(indexer)
		conjs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"keywords": []interface{}{"恭喜发财红包拿来", "坚决查处色情娱乐场所"},
		})
		convey.So(err, convey.ShouldBeNil)
		fmt.Println(FormatBitMapResult(conjs))

		scanner.Reset()
		conjs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"恭喜发财红包拿来"},
		})
		fmt.Println(FormatBitMapResult(conjs))

		scanner.Reset()
		conjs, err = scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
			"package": []interface{}{"坚决查处色情娱乐场所"},
		})
		fmt.Println(FormatBitMapResult(conjs))
	})
}
