package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
)

const (
	WildcardFieldName = BEField("__wildcard__")
)

var (
	wildcardQKey       = newQKey(WildcardFieldName, nil)
	defaultQueryOption = &RetrieveOption{}
)

type (
	FieldDesc struct {
		ID     uint64
		Field  BEField
		Parser parser.FieldValueParser
		option FieldOption
	}

	FieldOption struct {
		Parser    string
		Container string // specific the holder|container all tokenized value and correspondent Entries
	}

	IndexerSettings struct {
		EnableDebugMode bool
		FieldConfig     map[BEField]FieldOption
	}

	RetrieveContext struct {
		assigns Assignments
		option  *RetrieveOption
	}

	RetrieveOption struct {
		DumpStepInfo       bool
		DumpEntriesDetail  bool
		RemoveDuplicateDoc bool
	}

	BEIndex interface {
		//interface used by builder
		addWildcardEID(id EntryID)

		newFieldDescIfNeeded(field BEField) *FieldDesc

		newEntriesContainerIfNeeded(k int) *fieldEntriesContainer

		compileIndexer()

		// ConfigureIndexer public Interface
		ConfigureIndexer(settings *IndexerSettings)

		// Retrieve public Interface
		Retrieve(queries Assignments) (result DocIDList, err error)

		// RetrieveV2
		// Retrieve(queries Assignments, *RetrieveOption) (result DocIDList, err error)

		// DumpEntries debug api
		DumpEntries() string

		DumpEntriesSummary() string
	}

	indexBase struct {
		fieldDesc map[BEField]*FieldDesc

		idToField map[uint64]*FieldDesc

		// debug options
		settings *IndexerSettings // keep information for debug, it will consume more memory
	}
)

func (bi *indexBase) configureField(field BEField, option FieldOption) *FieldDesc {
	if _, ok := bi.fieldDesc[field]; ok {
		panic(fmt.Errorf("can't configure field twice, bz field id can only match one ID"))
	}

	valueParser := parser.NewParser(option.Parser)
	if valueParser == nil {
		Logger.Infof("not configure Parser for field:%s, use default", field)
		valueParser = parser.NewParser(parser.ParserNameCommon)
	}

	desc := &FieldDesc{
		Field:  field,
		Parser: valueParser,
		ID:     uint64(len(bi.fieldDesc)),
		option: option,
	}

	bi.fieldDesc[field] = desc
	bi.idToField[desc.ID] = desc
	Logger.Infof("configure field:%s, fieldID:%d\n", field, desc.ID)

	return desc
}

func (bi *indexBase) newFieldDescIfNeeded(field BEField) *FieldDesc {
	if desc, ok := bi.fieldDesc[field]; ok {
		return desc
	}
	return bi.configureField(field, FieldOption{
		Parser:    parser.ParserNameCommon,
		Container: HolderNameDefault,
	})
}

func (bi *indexBase) hasField(field BEField) bool {
	_, ok := bi.fieldDesc[field]
	return ok
}

func (bi *indexBase) getFieldFromID(v uint64) BEField {
	if field, ok := bi.idToField[v]; ok {
		return field.Field
	}
	return ""
}
