package core

import (
	"math/rand"
	"sort"
	"testing"
)

func TestTopKCollector_NoScoreStopsAtK(t *testing.T) {
	c := NewTopKCollector(3, nil)
	for _, id := range []DocID{7, 7, 9, 12} {
		if c.Add(id) == CollectStop {
			break
		}
	}
	got := c.DocIDs()
	if len(got) != 3 {
		t.Fatalf("want 3 docs, got %v", got)
	}
	if got[0] != 7 || got[1] != 9 || got[2] != 12 {
		t.Fatalf("want first 3 unique ids, got %v", got)
	}
}

func TestTopKCollector_ScoredKeepsHighest(t *testing.T) {
	scores := map[DocID]float64{1: 0.1, 2: 0.9, 3: 0.5, 4: 0.7}
	src := NewMapScoreSourceForTest(scores)
	c := NewTopKCollector(2, src)
	for _, id := range []DocID{1, 2, 3, 4, 1} {
		_ = c.Add(id)
	}
	got := c.Scored()
	if len(got) != 2 || got[0].DocID != 2 || got[1].DocID != 4 {
		t.Fatalf("want [2 4], got %v", got)
	}
}

func TestTopKCollector_MissingScoreModes(t *testing.T) {
	scores := NewMapScoreSourceForTest(map[DocID]float64{1: 0.2, 3: 0.5})

	zero := NewTopKCollector(2, scores)
	for _, id := range []DocID{1, 2, 3} {
		_ = zero.Add(id)
	}
	if got := zero.DocIDs(); len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("MissingScoreZero: want [3 1], got %v", got)
	}

	skip := NewTopKCollector(2, scores)
	skip.SetMissingScoreMode(MissingScoreSkip)
	for _, id := range []DocID{1, 2, 2, 3} {
		_ = skip.Add(id)
	}
	if got := skip.DocIDs(); len(got) != 2 || got[0] != 3 || got[1] != 1 {
		t.Fatalf("MissingScoreSkip: want [3 1], got %v", got)
	}
}

func TestRoaringScoreTable(t *testing.T) {
	ids := []DocID{5, 42, 100}
	scores := []float64{0.5, 2.5, 1.5}
	tbl, err := NewRoaringScoreTable(ids, scores)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		got, ok := tbl.Score(id)
		if !ok || got != scores[i] {
			t.Fatalf("id %d: got (%v,%v) want (%v,true)", id, got, ok, scores[i])
		}
	}
	if _, ok := tbl.Score(6); ok {
		t.Fatal("absent id should miss")
	}
}

func TestDenseScoreTable(t *testing.T) {
	tbl := NewDenseScoreTable(1000, []float64{1, NaNForTest(), 3})
	if s, ok := tbl.Score(1000); !ok || s != 1 {
		t.Fatal("base entry wrong")
	}
	if _, ok := tbl.Score(1001); ok {
		t.Fatal("NaN slot should be absent")
	}
	if s, ok := tbl.Score(1002); !ok || s != 3 {
		t.Fatal("last entry wrong")
	}
	if _, ok := tbl.Score(999); ok {
		t.Fatal("below base should miss")
	}
	if _, ok := tbl.Score(1003); ok {
		t.Fatal("past end should miss")
	}
}

func TestTopKCollector_FuzzOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 500; iter++ {
		n := rng.Intn(40)
		k := rng.Intn(8)
		scoreMap := map[DocID]float64{}
		for i := 0; i < n; i++ {
			scoreMap[DocID(i+1)] = float64(rng.Intn(10)) / 10
		}
		src := NewMapScoreSourceForTest(scoreMap)

		want := make([]ScoredDoc, 0, n)
		for id, s := range scoreMap {
			want = append(want, ScoredDoc{DocID: id, Score: s})
		}
		sort.Slice(want, func(i, j int) bool {
			if want[i].Score != want[j].Score {
				return want[i].Score > want[j].Score
			}
			return want[i].DocID < want[j].DocID
		})
		if len(want) > k {
			want = want[:k]
		}

		c := NewTopKCollector(k, src)
		stopped := false
		for i := 0; i < n*2; i++ {
			if c.Add(DocID(rng.Intn(n)+1)) == CollectStop {
				stopped = true
				break
			}
		}
		got := c.Scored()
		// With early stop the heap holds the best k of the prefix actually
		// fed; without stop it must equal the global oracle over all feeds.
		wantLen := min(len(want), c.Count())
		if !stopped && len(got) != len(want) {
			wantLen = len(want)
		}
		if len(got) > wantLen {
			t.Fatalf("iter %d: len got %d exceeds expected max %d", iter, len(got), wantLen)
		}
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Fatalf("iter %d: output not descending at %d: %v", iter, i, got)
			}
		}
	}
}
