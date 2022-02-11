package roaringidx

import (
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

type (
	// FieldSetting public settings for configuring a field
	FieldSetting struct {
		Parser    parser.FieldValueParser
		Container string
	}

	FieldMeta struct {
		FieldSetting

		field be_indexer.BEField
	}

	IvtBEIndexer struct {
		docMaxConjSize int

		data map[be_indexer.BEField]BEContainer
	}
)

func NewIvtBEIndexer() *IvtBEIndexer {
	return &IvtBEIndexer{
		data: make(map[be_indexer.BEField]BEContainer),
	}
}

func (meta *FieldMeta) FieldName() string {
	return string(meta.field)
}
