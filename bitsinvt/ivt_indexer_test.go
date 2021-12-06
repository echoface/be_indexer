package bitsinvt

import (
	"github.com/echoface/be_indexer"
	parser "github.com/echoface/be_indexer/parser/v2"
	"github.com/smartystreets/goconvey/convey"
	"testing"
)

func TestNewIvtBEIndexer(t *testing.T) {
	convey.Convey("test new Indexer", t, func() {
		indxer := NewIvtBEIndexer()
		convey.So(indxer, convey.ShouldNotBeNil)
	})
}

func TestIvtBEIndexer_ConfigureField(t *testing.T) {
	convey.Convey("test configure Indexer", t, func() {
		indexer := NewIvtBEIndexer()
		convey.So(indexer, convey.ShouldNotBeNil)

		convey.So(func() {
			indexer.ConfigureField("ad_id", FieldSetting{Parser: parser.ParserNamerNumber})
			indexer.ConfigureField("package", FieldSetting{Parser: parser.ParserNameStrHash})
		}, convey.ShouldNotPanic)

		convey.So(len(indexer.fields), convey.ShouldEqual, 2)
	})
}

func TestIvtBEIndexer_AddDocument(t *testing.T) {
	convey.Convey("test configure Indexer", t, func() {
		indexer := NewIvtBEIndexer()
		convey.So(indexer, convey.ShouldNotBeNil)

		indexer.ConfigureField("ad_id", FieldSetting{Parser: parser.ParserNamerNumber})
		indexer.ConfigureField("package", FieldSetting{Parser: parser.ParserNameStrHash})

		doc1 := NewDocument(1)
		doc1.AddConjunction(NewConjunction().
			Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
			Include("package", be_indexer.NewStrValues("com.echoface.be")))
		doc1.AddConjunction(NewConjunction().
			Include("package", be_indexer.NewStrValues("com.echoface.be")))

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

		indexer.AddDocuments(doc1, doc2, doc3, doc4)
	})
}
