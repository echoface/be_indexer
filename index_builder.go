package beindexer

import (
	"be_indexer/parser"
	"fmt"
)

type (
	IndexerBuilder struct {
		Documents map[int32]*Document
		settings  IndexerSettings
	}
)

func NewIndexerBuilder() *IndexerBuilder {
	return &IndexerBuilder{
		Documents: make(map[int32]*Document),
		settings: IndexerSettings{
			FieldConfig: make(map[BEField]FieldOption),
		},
	}
}

func (b *IndexerBuilder) SetFieldParser(field BEField, parserName string) {
	b.settings.FieldConfig[field] = FieldOption{
		Parser: parserName,
	}
}

func (b *IndexerBuilder) AddDocument(doc *Document) {
	if doc == nil {
		panic(fmt.Errorf("nil doc not allow"))
	}
	b.Documents[doc.ID] = doc
}

func (b *IndexerBuilder) RemoveDocument(doc int32) bool {
	_, hit := b.Documents[doc]
	if hit {
		delete(b.Documents, doc)
	}
	return hit
}

func (b *IndexerBuilder) buildDocEntries(indexer *BEIndex, doc *Document, parser parser.FieldValueParser) {

	doc.Prepare()

FORCONJ:
	for _, conj := range doc.Cons {

		if conj.size == 0 {
			indexer.wildcardEntries = append(indexer.wildcardEntries, NewEntryID(conj.id, true))
		}

		kSizeEntries := indexer.NewKSizeEntriesIfNeeded(conj.size)

		for field, expr := range conj.Expressions {

			desc := indexer.GetFieldDesc(field)

			var ids []uint64
			for _, value := range expr.Value {
				if res, e := desc.Parser.ParseValue(value); e == nil {
					ids = append(ids, res...)
				} else {
					Logger.Errorf("field %s parse failed\n", field)
					Logger.Errorf("value %+v parse fail detail:%+v\n", value, e)
					break FORCONJ
				}
			}

			fieldID := indexer.FieldID(field)
			entryID := NewEntryID(conj.id, expr.Incl)
			for _, id := range ids {
				kSizeEntries.AppendEntryID(NewKey(fieldID, id), entryID)
			}
		}
	}
}

func (b *IndexerBuilder) BuildIndex() *BEIndex {

	idGen := parser.NewIDAllocatorImpl()
	comParser := parser.NewCommonStrParser(idGen)

	indexer := NewBEIndex(idGen)

	indexer.ConfigureIndexer(&b.settings)

	for _, doc := range b.Documents {
		b.buildDocEntries(indexer, doc, comParser)
	}
	indexer.completeIndex()

	return indexer
}
