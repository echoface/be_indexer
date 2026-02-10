package roaringidx

import (
	"testing"

	"github.com/echoface/be_indexer"
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
				Container: ContainerNameDefault,
			})
			_ = indexer.ConfigureField("package", FieldSetting{
				Container: ContainerNameDefaultStr,
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
			Container: ContainerNameDefault,
		})
		_ = builder.ConfigureField("package", FieldSetting{
			Container: ContainerNameDefaultStr,
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

		err := builder.AddDocuments(doc1, doc2, doc3, doc4)
		convey.So(err, convey.ShouldBeNil)

		indexer, err := builder.BuildIndexer()
		convey.So(err, convey.ShouldBeNil)
		convey.So(indexer, convey.ShouldNotBeNil)
	})
}
