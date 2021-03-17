package beindexer

import (
	"be_indexer/parser"
	"fmt"
)

type (
	FieldOption struct {
		Parser string
	}
	IndexerSettings struct {
		FieldConfig map[string]FieldOption
	}

	IndexerBuilder struct {
		MaxK      int
		Documents map[int32]*Document
	}
)

func NewIndexerBuilder() *IndexerBuilder {
	return &IndexerBuilder{
		Documents: make(map[int32]*Document),
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

	for _, conj := range doc.Cons {

		kSizeEntries := indexer.NewKSizeEntriesIfNeeded(conj.size)

		if conj.size == 0 {
			kSizeEntries.AppendEntryID(indexer.wildCardKey(), NewEntryID(conj.id, true))
		}

		for field, expr := range conj.Expressions {
			ids, e := parser.ParseValue(expr.Value)
			if e != nil {
				fmt.Println("parse failed, value:", expr.Value, " e:", e.Error())
				break
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
	for _, doc := range b.Documents {

		fmt.Println("start gen entries for doc:", doc.ID)
		b.buildDocEntries(indexer, doc, comParser)

	}
	indexer.CompleteIndex()

	return indexer
}
