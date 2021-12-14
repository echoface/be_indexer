package roaringidx

import (
	"fmt"

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

func (builder *IvtBEIndexerBuilder) AddDocuments(docs ...*IndexDocument) {
	for _, doc := range docs {
		builder.AddDocument(doc)
	}
}

func (builder *IvtBEIndexerBuilder) AddDocument(doc *IndexDocument) {
	if !doc.Valid() {
		panic(fmt.Sprintf("invalid document: %s", doc.String()))
	}
	doc.ReIndexConjunction()

	if len(doc.Conjunctions) == 0 {
		// TODO: add to top-level wildcard document list ?
		return
	}

	for _, conj := range doc.Conjunctions {

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
				containerBuilder.EncodeWildcard(conj.id)
				continue
			}
			if err := containerBuilder.EncodeExpr(conj.id, expr); err != nil {
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
		indexer.data[field] = &fieldData{
			container: container,
		}
	}
	return indexer, nil
}
