package roaringidx

import (
	"github.com/echoface/be_indexer"
)

type (
	// FieldSetting public settings for configuring a field
	FieldSetting struct {
		Container string
	}

	FieldMeta struct {
		field be_indexer.BEField
		// Container type identifier
		Container string
	}

	IvtBEIndexer struct {
		docMaxConjSize int
		data           map[be_indexer.BEField]BEContainer
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
