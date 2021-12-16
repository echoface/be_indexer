package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/util"
)

type (
	IndexerBuilder struct {
		Documents  map[DocID]*Document
		settings   IndexerSettings
		skipBadDoc bool
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

func (b *IndexerBuilder) SetSkipBadDocument(skip bool) {
	b.skipBadDoc = skip
}

func (b *IndexerBuilder) SetFieldParser(field BEField, parserName string) {
	b.settings.FieldConfig[field] = FieldOption{
		Parser: parserName,
	}
}

func (b *IndexerBuilder) IndexSettings() *IndexerSettings {
	return &b.settings
}

func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption) {
	b.settings.FieldConfig[field] = settings
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

func (b *IndexerBuilder) buildDocEntries(indexer BEIndex, doc *Document) {
	util.PanicIf(len(doc.Cons) == 0, "no conjunctions in this document")
	util.PanicIf(len(doc.Cons) > 0xFF, "number of conjunction need less than 256")
CONJLoop:
	for idx, conj := range doc.Cons {

		incSize := conj.CalcConjSize()
		conjID := NewConjID(doc.ID, idx, incSize)

		if incSize == 0 {
			indexer.addWildcardEID(NewEntryID(conjID, true))
		}

		kSizeContainer := indexer.newEntriesContainerIfNeeded(incSize)

		for field, expr := range conj.Expressions {

			desc := indexer.newFieldDescIfNeeded(field)

			entryID := NewEntryID(conjID, expr.Incl)

			holder := kSizeContainer.newEntriesHolder(desc)

			if err := holder.AddFieldEID(desc, expr.Value, entryID); err != nil {

				Logger.Errorf("doc:%d field:%s AddFieldEID failed, values:%+v\n", doc.ID, field, expr.Value)

				util.PanicIf(!b.skipBadDoc, "AddFieldEID fail, field:%s, err:%+v", field, err)

				continue CONJLoop // break CONJLoop, conjunction as logic unit, just skip this conj if any error occur
			}
		}
	}
}

func (b *IndexerBuilder) BuildIndex() BEIndex {

	indexer := NewSizeGroupedBEIndex()

	indexer.ConfigureIndexer(&b.settings)

	for _, doc := range b.Documents {
		b.buildDocEntries(indexer, doc)
	}
	indexer.compileIndexer()

	return indexer
}

func (b *IndexerBuilder) BuildCompactedIndex() BEIndex {

	indexer := NewCompactedBEIndex()

	indexer.ConfigureIndexer(&b.settings)

	for _, doc := range b.Documents {
		b.buildDocEntries(indexer, doc)
	}
	indexer.compileIndexer()

	return indexer
}
