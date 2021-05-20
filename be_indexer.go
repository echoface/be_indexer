package be_indexer

import (
	"fmt"
	"github.com/echoface/be_indexer/parser"
)

type (
	FieldDesc struct {
		ID     uint64
		Field  BEField
		Parser parser.FieldValueParser
	}

	FieldOption struct {
		Parser string
	}

	IndexerSettings struct {
		FieldConfig map[BEField]FieldOption
	}

	RetrieveContext struct {
		idAssigns map[BEField][]uint64
	}

	BEIndex interface {
		//interface used by builder
		appendWildcardEntryID(id EntryID)
		newFieldDescIfNeeded(field BEField) *FieldDesc
		newPostingEntriesIfNeeded(k int) *PostingEntries
		completeIndex()

		//ConfigureIndexer public Interface
		ConfigureIndexer(settings *IndexerSettings)
		Retrieve(queries Assignments) (result DocIDList, err error)

		//DumpEntries debug api
		DumpEntries() string
		DumpEntriesSummary() string
	}

	indexBase struct {
		idAllocator parser.IDAllocator
		fieldDesc   map[BEField]*FieldDesc
		idToField   map[uint64]*FieldDesc
	}
)

func (bi *indexBase) configureField(field BEField, option FieldOption) *FieldDesc {
	if _, ok := bi.fieldDesc[field]; ok {
		panic(fmt.Errorf("can't configure field twice, bz field id can only match one ID"))
	}

	valueParser := parser.NewParser(option.Parser, bi.idAllocator)
	if valueParser == nil {
		valueParser = parser.NewParser(parser.CommonParser, bi.idAllocator)
	}
	desc := &FieldDesc{
		Field:  field,
		Parser: valueParser,
		ID:     uint64(len(bi.fieldDesc)),
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
		Parser: parser.CommonParser,
	})
}

func (bi *indexBase) hasField(field BEField) bool {
	_, ok := bi.fieldDesc[field]
	return ok
}

func (bi *indexBase) StringKey(key Key) string {
	return fmt.Sprintf("<%s,%d>", bi.getFieldFromID(key.GetFieldID()), key.GetValueID())
}

func (bi *indexBase) getFieldFromID(v uint64) BEField {
	if field, ok := bi.idToField[v]; ok {
		return field.Field
	}
	return ""
}

// parse queries value to value id list
func (bi *indexBase) parseQueries(queries Assignments) (map[BEField][]uint64, error) {
	idAssigns := make(map[BEField][]uint64, len(queries))

	for field, values := range queries {
		if !bi.hasField(field) {
			continue
		}

		desc, ok := bi.fieldDesc[field]
		if !ok { //no such field, ignore it(ps: bz it will not match any doc)
			continue
		}

		res := make([]uint64, 0, len(values))
		for _, value := range values {
			ids, err := desc.Parser.ParseAssign(value)
			if err != nil {
				Logger.Errorf("field:%s, value:%+v can't be parsed, err:%s\n", field, value, err.Error())
				return nil, fmt.Errorf("query assign parse fail,field:%s e:%s\n", field, err.Error())
			}
			res = append(res, ids...)
		}
		idAssigns[field] = res
	}
	return idAssigns, nil
}
