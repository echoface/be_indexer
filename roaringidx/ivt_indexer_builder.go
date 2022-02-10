package roaringidx

import (
	"fmt"

	"github.com/echoface/be_indexer/parser"

	"github.com/echoface/be_indexer/util"

	"github.com/echoface/be_indexer"
)

type (
	IvtBEIndexerBuilder struct {
		// panicOnError will panic program when build indexer meta
		panicOnError bool

		containerBuilder map[be_indexer.BEField]BEContainerBuilder

		strIDGen parser.IDAllocator
	}
)

func NewIndexerBuilder() *IvtBEIndexerBuilder {
	return &IvtBEIndexerBuilder{
		panicOnError:     false,
		strIDGen:         parser.NewIDAllocatorImpl(),
		containerBuilder: map[be_indexer.BEField]BEContainerBuilder{},
	}
}

func (builder *IvtBEIndexerBuilder) WithIDGen(gen parser.IDAllocator) *IvtBEIndexerBuilder {
	builder.strIDGen = gen
	return builder
}

func (builder *IvtBEIndexerBuilder) WithErrPanic(panic bool) *IvtBEIndexerBuilder {
	builder.panicOnError = panic
	return builder
}

func (builder *IvtBEIndexerBuilder) ConfigureField(field string, option FieldSetting) error {
	fieldMeta := &FieldMeta{
		FieldSetting: option,
		field:        be_indexer.BEField(field),
	}
	fieldContainerBuilder := NewContainerBuilder(fieldMeta)
	if fieldContainerBuilder.NeedParser() && option.Parser == nil {
		fieldMeta.Parser = parser.NewCommonParserWithAllocator(builder.strIDGen)
	}
	if fieldContainerBuilder == nil {
		util.PanicIf(builder.panicOnError, "field:%s settings:%+v not supported", field, option)
		return fmt.Errorf("field:%s settings:%+v not supported", field, option)
	}

	builder.containerBuilder[be_indexer.BEField(field)] = fieldContainerBuilder
	return nil
}

func (builder *IvtBEIndexerBuilder) AddDocuments(docs ...*be_indexer.Document) (err error) {
	for _, doc := range docs {
		err = builder.AddDocument(doc)
		if err != nil {
			return err
		}
	}
	return nil
}

func (builder *IvtBEIndexerBuilder) AddDocument(doc *be_indexer.Document) (err error) {

	if doc == nil || len(doc.Cons) == 0 {
		util.PanicIf(builder.panicOnError, "zero conjunction in this document")
		return fmt.Errorf("empty document(zero conjunctions) is not allowed")
	}

	for idx, conj := range doc.Cons {

		var conjID ConjunctionID
		if conjID, err = NewConjunctionID(idx, int64(doc.ID)); err != nil {
			be_indexer.Logger.Errorf("gen conjunction id for doc:%d fail, err:%s", doc.ID, err.Error())
			util.PanicIf(builder.panicOnError, "gen conj id fail,%v", err)
			return err
		}

		// NOTE: check conjunction contains none-configured field expression
		// this may case logic error if we omit those boolean-expression
		for field := range conj.Expressions {
			if _, ok := builder.containerBuilder[field]; !ok {
				util.PanicIf(builder.panicOnError, "document contains none-configured field:%s", field)
				be_indexer.LogErrIf(true, "document contains none-configured field:%", field)
				return fmt.Errorf("document contains none-configured field:%s", field)
			}
		}

		for field, containerBuilder := range builder.containerBuilder {
			expr, ok := conj.Expressions[field]
			if !ok {
				containerBuilder.EncodeWildcard(conjID)
				continue
			}

			if err = containerBuilder.EncodeExpr(conjID, be_indexer.NewBoolExpr2(field, *expr)); err != nil {
				util.PanicIf(builder.panicOnError, "failed evaluate boolean expression:%+v", expr)
				return err
			}
		}
	}
	return nil
}

func (builder *IvtBEIndexerBuilder) BuildIndexer() (*IvtBEIndexer, error) {

	indexer := NewIvtBEIndexer()

	for field, fieldBuilder := range builder.containerBuilder {
		container, err := fieldBuilder.BuildBEContainer()
		if err != nil {
			return nil, err
		}
		indexer.data[field] = container
	}
	return indexer, nil
}
