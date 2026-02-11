package roaringidx

import (
	"github.com/echoface/be_indexer/core"
	"testing"

	"github.com/echoface/be_indexer/parser"

	"github.com/smartystreets/goconvey/convey"
)

func TestNewIvtBEIndexer(t *testing.T) {
	convey.Convey("test new Indexer", t, func() {
		indexer := NewIvtBEIndexer()
		convey.So(indexer, convey.ShouldNotBeNil)
	})
}

func TestIvtBEIndexer_ConfigureField(t *testing.T) {
	convey.Convey("test configure Indexer", t, func() {
		indexer := NewIndexerBuilder()
		convey.So(indexer, convey.ShouldNotBeNil)

		convey.So(func() {
			_ = indexer.ConfigureField("ad_id", FieldSetting{
				Parser:    parser.NewNumberParser(),
				Container: "default",
			})
			_ = indexer.ConfigureField("package", FieldSetting{
				Container: "default",
				Parser:    parser.NewNumberParser(),
			})
		}, convey.ShouldNotPanic)

		convey.So(len(indexer.containerBuilder), convey.ShouldEqual, 2)
	})
}

func TestIvtBEIndexer_AddDocument(t *testing.T) {
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

		doc1 := core.NewDocument(1)
		doc1.AddConjunction(core.NewConjunction().
			Include("ad_id", core.NewIntValues(100, 101, 108)).
			Include("package", core.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(core.NewConjunction().
			Include("package", core.NewStrValues("com.echoface.x")))

		doc2 := core.NewDocument(5)
		doc2.AddConjunction(core.NewConjunction().
			Include("ad_id", core.NewIntValues(100, 101, 108)).
			Include("package", core.NewStrValues("com.echoface.be")))
		doc2.AddConjunction(core.NewConjunction().
			Exclude("package", core.NewStrValues("com.echoface.not")))

		doc3 := core.NewDocument(20)
		doc3.AddConjunctions(core.NewConjunction())

		doc4 := core.NewDocument(50)
		doc4.AddConjunction(core.NewConjunction().
			Exclude("ad_id", core.NewIntValues(100, 108)).
			Include("package", core.NewStrValues("com.echoface.be")))

		err := builder.AddDocuments(doc1, doc2, doc3, doc4)
		convey.So(err, convey.ShouldBeNil)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)
	})
}
