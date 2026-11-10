package be_indexer

import "github.com/echoface/be_indexer/core"

type (
	TopKCollector          = core.TopKCollector
	ScoreSource            = core.ScoreSource
	ScoredDoc              = core.ScoredDoc
	AtomicScoreSource      = core.AtomicScoreSource
	DenseScoreTable        = core.DenseScoreTable
	RoaringScoreTable      = core.RoaringScoreTable
	BlockMaxScoreTable     = core.BlockMaxScoreTable
	CompetitiveScoreSource = core.CompetitiveScoreSource
	CompetitiveCollector   = core.CompetitiveCollector
	MissingScoreMode       = core.MissingScoreMode
)

const (
	MissingScoreZero = core.MissingScoreZero
	MissingScoreSkip = core.MissingScoreSkip
)

// NewTopKCollector creates a reusable collector. With scores it returns exact
// best-k matches; with nil scores it stops at any first k unique matches.
func NewTopKCollector(k int, scores ScoreSource) *TopKCollector {
	return core.NewTopKCollector(k, scores)
}

// NewDenseScoreTable copies scores for a contiguous DocID range.
func NewDenseScoreTable(base DocID, scores []float64) *DenseScoreTable {
	return core.NewDenseScoreTable(base, scores)
}

// NewRoaringScoreTable copies scores indexed by strictly ascending signed IDs.
func NewRoaringScoreTable(ids []DocID, scores []float64) (*RoaringScoreTable, error) {
	return core.NewRoaringScoreTable(ids, scores)
}

// NewBlockMaxScoreTable builds an immutable score sidecar with certified skip bounds.
func NewBlockMaxScoreTable(ids []DocID, scores []float64) (*BlockMaxScoreTable, error) {
	return core.NewBlockMaxScoreTable(ids, scores)
}
