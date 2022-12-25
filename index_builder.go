package be_indexer

import (
	"fmt"

	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

type (
	IndexerBuilder struct {
		indexer BEIndex

		fieldsData map[BEField]*FieldDesc

		idAllocator parser.IDAllocator

		// 是否允许一个doc中部分Conjunction解析失败
		badConjBehavior BadConjBehavior
	}

	BuilderOpt func(builder *IndexerBuilder)

	BadConjBehavior int
)

const (
	ErrorBadConj = 0
	SkipBadConj  = 1
	PanicBadConj = 2
)

func WithBadConjBehavior(v BadConjBehavior) BuilderOpt {
	return func(builder *IndexerBuilder) {
		builder.badConjBehavior = v
	}
}

func NewIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder {
	builder := &IndexerBuilder{
		indexer:     NewSizeGroupedBEIndex(),
		fieldsData:  map[BEField]*FieldDesc{},
		idAllocator: parser.NewIDAllocatorImpl(),
	}
	_, _ = builder.configureField(WildcardFieldName, FieldOption{
		Container: HolderNameDefault,
	})
	for _, optFn := range opts {
		optFn(builder)
	}
	return builder
}

func NewCompactIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder {
	builder := &IndexerBuilder{
		indexer:     NewCompactedBEIndex(),
		fieldsData:  map[BEField]*FieldDesc{},
		idAllocator: parser.NewIDAllocatorImpl(),
	}
	_, _ = builder.configureField(WildcardFieldName, FieldOption{
		Container: HolderNameDefault,
	})
	for _, optFn := range opts {
		optFn(builder)
	}
	return builder
}

func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption) {
	_, err := b.configureField(field, settings)
	util.PanicIfErr(err, "config field:%s with option fail:%+v", field, settings)
}

func (b *IndexerBuilder) AddDocument(docs ...*Document) error {
	for _, doc := range docs {
		util.PanicIf(doc == nil, "nil document not be allowed")
		if err := b.validDocument(doc); err != nil {
			return err
		}
		if err := b.buildDocEntries(doc); err != nil {
			return err
		}
	}
	return nil
}

func (b *IndexerBuilder) BuildIndex() BEIndex {

	b.indexer.setFieldDesc(b.fieldsData)

	err := b.indexer.compileIndexer()
	util.PanicIfErr(err, "fail compile indexer data, err:%+v", err)

	return b.indexer
}

func (b *IndexerBuilder) configureField(field BEField, option FieldOption) (*FieldDesc, error) {
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
	desc := &FieldDesc{
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

func (b *IndexerBuilder) createFieldData(field BEField) *FieldDesc {
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

ConjLoop:
	for idx, conj := range doc.Cons {

		incSize := conj.CalcConjSize()
		conjID := NewConjID(doc.ID, idx, incSize)

		if incSize == 0 {
			b.indexer.addWildcardEID(NewEntryID(conjID, true))
		}

		container := b.indexer.newContainer(incSize)

		conjStatements := map[string]*Preparation{}

		for field, expr := range conj.Expressions {

			desc := b.createFieldData(field)

			entryID := NewEntryID(conjID, expr.Incl)

			holder := container.newEntriesHolder(desc)

			if preparation, err := holder.PrepareAppend(desc, expr); err != nil {
				if b.badConjBehavior == SkipBadConj {
					Logger.Errorf("doc:%d holder.PrepareAppend field:%s fail:%v\n", doc.ID, field, err)
					continue ConjLoop
				} else if b.badConjBehavior == ErrorBadConj {
					return fmt.Errorf("doc:%d holder.PrepareAppend field:%s fail:%v", doc.ID, field, err)
				}
				panic(fmt.Errorf("doc:%d holder.PrepareAppend field:%s fail:%v", doc.ID, field, err))
			} else {
				preparation.field = desc
				preparation.holder = holder
				preparation.entryID = entryID
				conjStatements[string(desc.Field)] = &preparation
			}
		}
		for _, preparation := range conjStatements {
			preparation.holder.CommitAppend(preparation, preparation.entryID)
		}
	}
	return nil
}
