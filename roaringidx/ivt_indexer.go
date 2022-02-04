package roaringidx

import (
	"github.com/echoface/be_indexer"
)

type (
	// FieldSetting public settings for configuring a field
	FieldSetting struct {
		Parser    string `json:"parser" yaml:"parser"`
		Container string `json:"container" yaml:"container"`
	}

	fieldMeta struct {
		container BEContainer

		field be_indexer.BEField
	}

	IvtBEIndexer struct {
		data map[be_indexer.BEField]*fieldMeta
	}
)

func NewIvtBEIndexer() *IvtBEIndexer {
	return &IvtBEIndexer{
		data: make(map[be_indexer.BEField]*fieldMeta),
	}
}
