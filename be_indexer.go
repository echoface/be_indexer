package be_indexer

import (
	"github.com/echoface/be_indexer/parser"
)

const (
	WildcardFieldName = BEField("_Z_")
)

var (
	wildcardQKey       = newQKey(WildcardFieldName, 0)
	defaultQueryOption = RetrieveOption{}
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
		option  RetrieveOption
	}

	RetrieveOption struct {
		DumpStepInfo       bool
		DumpEntriesDetail  bool
		RemoveDuplicateDoc bool
	}

	IndexOpt func(ctx *RetrieveContext)

	BEIndex interface {
		// addWildcardEID interface used by builder
		addWildcardEID(id EntryID)

		setFieldDesc(fieldsData map[BEField]*FieldDesc)

		// newContainer indexer need return a valid Container for k size
		newContainer(k int) *fieldEntriesContainer

		// compileIndexer prepare indexer and optimize index data
		compileIndexer()

		// Retrieve public Interface
		Retrieve(queries Assignments, opt ...IndexOpt) (result DocIDList, err error)

		// DumpEntries debug api
		DumpEntries() string

		DumpEntriesSummary() string
	}

	indexBase struct {
		// fieldsData a field settings and resource, if not configured, it will use default parser and container
		// for expression values;
		fieldsData map[BEField]*FieldDesc

		// wildcardEntries hold all entry id that conjunction size is zero;
		wildcardEntries Entries
	}
)

func WithStepDetail() IndexOpt {
	return func(ctx *RetrieveContext) {
		ctx.option.DumpStepInfo = true
	}
}

func WithDumpEntries() IndexOpt {
	return func(ctx *RetrieveContext) {
		ctx.option.DumpEntriesDetail = true
	}
}

func (bi *indexBase) setFieldDesc(fieldsData map[BEField]*FieldDesc) {
	bi.fieldsData = fieldsData
}

// addWildcardEID append wildcard entry id to Z set
func (bi *indexBase) addWildcardEID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}
