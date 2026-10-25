package core

import "math"

func NewMapScoreSourceForTest(m map[DocID]float64) ScoreSource {
	return mapScoreSource(m)
}

func NaNForTest() float64 { return math.NaN() }

type mapScoreSource map[DocID]float64

func (m mapScoreSource) Score(id DocID) (float64, bool) {
	s, ok := m[id]
	return s, ok
}
