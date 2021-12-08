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

	fieldData struct {
		container BEContainer
	}

	IvtBEIndexer struct {
		data map[be_indexer.BEField]*fieldData
	}
)

func NewIvtBEIndexer() *IvtBEIndexer {
	return &IvtBEIndexer{
		data: make(map[be_indexer.BEField]*fieldData),
	}
}
