package be_indexer

import (
	"fmt"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

type (
	IndexerBuilder struct {
		indexer BEIndex

		fieldsData map[BEField]*fieldDesc

		idAllocator parser.IDAllocator
	}
)

func NewIndexerBuilder() *IndexerBuilder {
	builder := &IndexerBuilder{
		indexer:     NewSizeGroupedBEIndex(),
		fieldsData:  map[BEField]*fieldDesc{},
		idAllocator: parser.NewIDAllocatorImpl(),
	}
	_, _ = builder.configureField(WildcardFieldName, FieldOption{
		Container: HolderNameDefault,
	})
	return builder
}

func NewCompactIndexerBuilder() *IndexerBuilder {
	builder := &IndexerBuilder{
		indexer:     NewCompactedBEIndex(),
		fieldsData:  map[BEField]*fieldDesc{},
		idAllocator: parser.NewIDAllocatorImpl(),
	}
	_, _ = builder.configureField(WildcardFieldName, FieldOption{
		Container: HolderNameDefault,
	})
	return builder
}

func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption) {
	_, err := b.configureField(field, settings)
	util.PanicIfErr(err, "config field:%s with option fail:%+v", field, settings)
}

func (b *IndexerBuilder) AddDocument(doc *Document) error {
	util.PanicIf(doc == nil, "nil document not be allowed")
	if err := b.validDocument(doc); err != nil {
		return err
	}
	return b.buildDocEntries(doc)
}

func (b *IndexerBuilder) BuildIndex() BEIndex {

	b.indexer.setFieldDesc(b.fieldsData)

	b.indexer.compileIndexer()

	return b.indexer
}

func (b *IndexerBuilder) configureField(field BEField, option FieldOption) (*fieldDesc, error) {
	if _, ok := b.fieldsData[field]; ok {
		return nil, fmt.Errorf("can't configure field:%s twice", field)
	}
	if option.Parser == nil {
		option.Parser = parser.NewCommonParserWithAllocator(b.idAllocator)
		Logger.Infof("not configure Parser for field:%s, use default", field)
	}
	if len(option.Container) == 0 {
		option.Container = HolderNameDefault
		Logger.Infof("not configure container for field:%s, use default", field)
	}

	fieldID := uint64(len(b.fieldsData))
	desc := &fieldDesc{
		FieldOption: option,
		Field:       field,
		ID:          fieldID,
	}
	b.fieldsData[field] = desc
	Logger.Infof("configure field:%s, fieldID:%d\n", field, desc.ID)
	return desc, nil
}

func (b *IndexerBuilder) validDocument(doc *Document) error {
	// util.PanicIf(len(doc.Cons) == 0, "no conjunctions in this document")
	// util.PanicIf(len(doc.Cons) > 0xFF, "number of conjunction need less than 256")
	if len(doc.Cons) == 0 {
		return fmt.Errorf("no conjunctions in this document")
	}
	if len(doc.Cons) > 0xFF {
		return fmt.Errorf("number of conjunction need less than 256")
	}
	return nil
}

func (b *IndexerBuilder) createFieldData(field BEField) *fieldDesc {
	if desc, hit := b.fieldsData[field]; hit {
		return desc
	}
	desc, err := b.configureField(field, FieldOption{
		Container: HolderNameDefault,
	})
	util.PanicIfErr(err, "this should not happened for default settings")
	return desc
}

func (b *IndexerBuilder) buildDocEntries(doc *Document) error {
	util.PanicIf(len(doc.Cons) == 0, "no conjunctions in this document")
	util.PanicIf(len(doc.Cons) > 0xFF, "number of conjunction need less than 256")

	for idx, conj := range doc.Cons {

		incSize := conj.CalcConjSize()
		conjID := NewConjID(doc.ID, idx, incSize)

		if incSize == 0 {
			b.indexer.addWildcardEID(NewEntryID(conjID, true))
		}

		container := b.indexer.newContainer(incSize)

		for field, expr := range conj.Expressions {

			desc := b.createFieldData(field)

			entryID := NewEntryID(conjID, expr.Incl)

			holder := container.newEntriesHolder(desc)

			if err := holder.AddFieldEID(desc, expr.Value, entryID); err != nil {
				Logger.Errorf("doc:%d field:%s AddFieldEID failed, values:%+v\n", doc.ID, field, expr.Value)
				return err
				// continue CONJLoop // break CONJLoop, conjunction as logic unit, just skip this conj if any error occur
			}
		}
	}
	return nil
}
