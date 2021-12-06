package bitsinvt

import (
	"fmt"
	"github.com/echoface/be_indexer"
	parser2 "github.com/echoface/be_indexer/parser/v2"
)

type (
	// FieldSetting public settings for configuring a field
	FieldSetting struct {
		Parser string `json:"parser" yaml:"parser"`
	}

	fieldData struct {
		setting FieldSetting
		indexPl *BEFieldContainer
		parser  parser2.FieldValueParser
	}

	IvtBEIndexer struct {
		fields map[be_indexer.BEField]*fieldData
	}
)

func NewIvtBEIndexer() *IvtBEIndexer {
	return &IvtBEIndexer{
		fields: make(map[be_indexer.BEField]*fieldData),
	}
}

func (indexer *IvtBEIndexer) ConfigureField(field string, option FieldSetting) {
	if _, ok := indexer.fields[be_indexer.BEField(field)]; ok {
		panic(fmt.Errorf("can't configure field twice, field:%s", field))
	}
	valueParser := parser2.NewParser(option.Parser)
	if valueParser == nil {
		panic(fmt.Errorf("can't configure field, parser:%s not found", option.Parser))
	}
	indexer.fields[be_indexer.BEField(field)] = &fieldData{
		setting: option,
		parser:  valueParser,
		indexPl: NewFieldContainer(),
	}
}

func (indexer *IvtBEIndexer) AddDocuments(docs ...*IndexDocument) {
	for _, doc := range docs {
		indexer.AddDocument(doc)
	}
}

func (indexer *IvtBEIndexer) AddDocument(doc *IndexDocument) {
	if !doc.Valid() {
		panic(fmt.Sprintf("invalid document: %s", doc.String()))
	}
	doc.ReIndexConjunction()
	for field, fieldData := range indexer.fields {
		vParser := fieldData.parser
		container := fieldData.indexPl

		for _, conj := range doc.Conjunctions {
			exprs, hit := conj.Expressions[field]
			if !hit {
				container.addWildcard(conj.id)
				continue
			}
			for _, value := range exprs.Value {
				valueIDs, err := vParser.ParseValue(value)
				if err != nil {
					panic(fmt.Errorf("value:%+v can't be parsed", value))
				}
				for _, fv := range valueIDs {
					if exprs.Incl {
						container.AddInclude(BEValue(fv), conj.id)
					} else {
						container.AddExclude(BEValue(fv), conj.id)
					}
				}
			}
		}
	}
}
