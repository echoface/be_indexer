package roaringidx

import (
	"fmt"

	"github.com/echoface/be_indexer/util"

	"github.com/echoface/be_indexer"
)

type (
	IvtBEIndexerBuilder struct {
		containerBuilder map[be_indexer.BEField]BEContainerBuilder
	}
)

func NewIndexerBuilder() *IvtBEIndexerBuilder {
	return &IvtBEIndexerBuilder{
		containerBuilder: map[be_indexer.BEField]BEContainerBuilder{},
	}
}

func (builder *IvtBEIndexerBuilder) ConfigureField(field string, option FieldSetting) {
	fieldContainerBuilder := NewContainerBuilder(option.Container, option)
	if fieldContainerBuilder == nil {
		panic(fmt.Errorf("field:%s setttings:%+v not supported", field, option))
	}

	builder.containerBuilder[be_indexer.BEField(field)] = fieldContainerBuilder
}

func (builder *IvtBEIndexerBuilder) AddDocuments(docs ...*be_indexer.Document) {
	for _, doc := range docs {
		builder.AddDocument(doc)
	}
}

func (builder *IvtBEIndexerBuilder) AddDocument(doc *be_indexer.Document) {
	util.PanicIf(len(doc.Cons) == 0, "zero conjunction in this document")

	for idx, conj := range doc.Cons {

		conjID := NewConjunctionID(idx, int64(doc.ID))
		// NOTE: check conjunction contains none-configured field expression
		// this may case logic error if we omit those boolean-expression
		for field := range conj.Expressions {
			if _, ok := builder.containerBuilder[field]; !ok {
				panic(fmt.Errorf("document contains none-configured field:%s", field))
			}
		}

		for field, containerBuilder := range builder.containerBuilder {
			expr, ok := conj.Expressions[field]
			if !ok {
				containerBuilder.EncodeWildcard(conjID)
				continue
			}
			if err := containerBuilder.EncodeExpr(conjID, be_indexer.NewBoolExpr2(field, *expr)); err != nil {
				panic(fmt.Errorf("faild evaluate boolean expression:%+v", expr))
			}
		}
	}
}

func (builder *IvtBEIndexerBuilder) BuildIndexer() (*IvtBEIndexer, error) {
	indexer := NewIvtBEIndexer()
	for field, builder := range builder.containerBuilder {
		container, err := builder.BuildBEContainer()
		if err != nil {
			return nil, err
		}
		indexer.data[field] = &fieldMeta{
			container: container,
		}
	}
	return indexer, nil
}
