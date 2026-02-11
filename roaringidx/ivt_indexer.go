package roaringidx

import (
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
)

type (
	// FieldSetting public settings for configuring a field
	FieldSetting struct {
		Parser    parser.ValueIDGenerator
		Container string
	}

	FieldMeta struct {
		FieldSetting

		field core.BEField
	}

	IvtBEIndexer struct {
		docMaxConjSize int
		data           map[core.BEField]BEContainer
	}
)

func NewIvtBEIndexer() *IvtBEIndexer {
	return &IvtBEIndexer{
		data: make(map[core.BEField]BEContainer),
	}
}

func (meta *FieldMeta) FieldName() string {
	return string(meta.field)
}
