package core

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// BenchmarkTopKFill isolates collection before and across the first full heap.
// Reset and Score are timed; construction and the final owned result sort are
// excluded. In particular, Reset still clears the O(limit) preallocated set.
func BenchmarkTopKFill(b *testing.B) {
	const matches = 4096
	for _, distribution := range []string{"ascending", "descending", "random"} {
		values := make([]float64, matches)
		rng := rand.New(rand.NewSource(91))
		for i := range values {
			switch distribution {
			case "ascending":
				values[i] = float64(i)
			case "descending":
				values[i] = float64(matches - i)
			default:
				values[i] = rng.Float64()
			}
		}
		source := NewDenseScoreTable(0, values)
		want := make([]ScoredDoc, matches)
		for i, score := range values {
			want[i] = ScoredDoc{DocID: DocID(i), Score: score}
		}
		sort.Slice(want, func(i, j int) bool {
			if want[i].Score != want[j].Score {
				return want[i].Score > want[j].Score
			}
			return want[i].DocID < want[j].DocID
		})
		for _, limit := range []int{512, matches, matches * 4} {
			b.Run(fmt.Sprintf("%s/matches_%d/limit_%d", distribution, matches, limit), func(b *testing.B) {
				c := NewTopKCollector(limit, source)
				for id := DocID(0); id < matches; id++ {
					c.Add(id)
				}
				got := c.Scored()
				if len(got) != min(limit, matches) {
					b.Fatal("incorrect result count")
				}
				for i, d := range got {
					if d != want[i] {
						b.Fatal("ranking differs from full sort")
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.Reset(source)
					for id := DocID(0); id < matches; id++ {
						c.Add(id)
					}
				}
			})
		}
	}
}
