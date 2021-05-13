package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
)

type (
	IndexerBuilder struct {
		Documents map[DocID]*Document
		settings  IndexerSettings
	}
)

func NewIndexerBuilder() *IndexerBuilder {
	return &IndexerBuilder{
		Documents: make(map[DocID]*Document),
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

func (b *IndexerBuilder) RemoveDocument(doc DocID) bool {
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
		type pair struct {
			key   Key
			entry EntryID
		}
		var pairs []*pair

		for field, expr := range conj.Expressions {
			desc := indexer.GetOrNewFieldDesc(field)
			entryID := NewEntryID(conj.id, expr.Incl)

			for _, value := range expr.Value {
				res, e := desc.Parser.ParseValue(value)
				if e != nil {
					Logger.Errorf("field %s parse failed\n", field)
					Logger.Errorf("doc:%d, value %+v parse fail err detail:%+v\n", conj.id.DocID(), value, e)
					continue FORCONJ //break FORCONJ, conjunction as logic unit, just skip this conj if any error occur
				}
				for _, id := range res {
					pairs = append(pairs, &pair{
						key:   NewKey(desc.ID, id),
						entry: entryID,
					})
				}
			}
		}
		for _, v := range pairs {
			kSizeEntries.AppendEntryID(v.key, v.entry)
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
