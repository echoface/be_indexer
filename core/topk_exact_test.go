package core

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTopKExactRanking(t *testing.T) {
	Convey("scored collection is global, deterministic and reusable", t, func() {
		rng := rand.New(rand.NewSource(91))
		for trial := 0; trial < 100; trial++ {
			scores := mapScoreSource{}
			for id := DocID(-80); id <= 80; id++ {
				scores[id] = float64(rng.Intn(9) - 4)
			}
			scores[-80] = math.Inf(1)
			scores[80] = math.Inf(-1)
			scores[0] = math.NaN()
			k := 1 + rng.Intn(40)
			for _, mode := range []MissingScoreMode{MissingScoreZero, MissingScoreSkip} {
				c := NewTopKCollector(k, scores)
				c.SetMissingScoreMode(mode)
				for round := 0; round < 2; round++ {
					c.Reset(scores)
					fed := map[DocID]bool{}
					for i := 0; i < 1000; i++ {
						id := DocID(rng.Intn(181) - 90)
						fed[id] = true
						So(c.Add(id), ShouldEqual, CollectContinue)
					}
					want := []ScoredDoc{}
					for id := range fed {
						score, ok := scores[id]
						if !ok || math.IsNaN(score) {
							if mode == MissingScoreSkip {
								continue
							}
							score = 0
						}
						want = append(want, ScoredDoc{DocID: id, Score: score})
					}
					sort.Slice(want, func(i, j int) bool {
						if want[i].Score != want[j].Score {
							return want[i].Score > want[j].Score
						}
						return want[i].DocID < want[j].DocID
					})
					want = want[:min(k, len(want))]
					So(c.Scored(), ShouldResemble, want)
					So(c.Count(), ShouldEqual, len(want))
				}
			}
		}
	})
}

func TestTopKCollectionAllocations(t *testing.T) {
	Convey("append, first heapify, replacement and reuse allocate nothing", t, func() {
		values := make([]float64, 4096)
		for i := range values {
			values[i] = float64(i)
		}
		src := NewDenseScoreTable(-2048, values)
		for _, limit := range []int{1, 31, 4096, 8192} {
			c := NewTopKCollector(limit, src)
			allocations := testing.AllocsPerRun(20, func() {
				c.Reset(src)
				for id := DocID(-2048); id < 2048; id++ {
					c.Add(id)
					c.Add(id)
				}
			})
			So(allocations, ShouldEqual, 0)
			So(c.Count(), ShouldEqual, min(limit, 4096))
			So(c.Scored()[0].DocID, ShouldEqual, 2047)
		}
	})
}

// boundProbe detects whether an incomplete result incorrectly triggers pruning.
type boundProbe struct {
	ScoreSource
	calls     int
	threshold ScoredDoc
}

func (p *boundProbe) NextCompetitive(entry EntryID, threshold ScoredDoc, _ MissingScoreMode) EntryID {
	p.calls++
	p.threshold = threshold
	return entry
}

func TestTopKFirstFullThreshold(t *testing.T) {
	Convey("only distinct eligible results establish the first threshold", t, func() {
		scores := &boundProbe{ScoreSource: mapScoreSource{10: 5, 20: 1, 30: 3, 40: 8, -10: 3, 50: math.NaN()}}
		c := NewTopKCollector(3, scores)
		c.SetMissingScoreMode(MissingScoreSkip)
		entry := NewEntryID(NewConjID(20, 0, 1), false)
		for _, id := range []DocID{10, 20, 10, 50, 99} {
			So(c.Add(id), ShouldEqual, CollectContinue)
			_, full := c.Threshold()
			So(full, ShouldBeFalse)
			So(c.NextCandidate(entry), ShouldEqual, entry)
		}
		So(scores.calls, ShouldEqual, 0)
		So(c.Count(), ShouldEqual, 2)
		partial := c.Scored()
		So(partial, ShouldResemble, []ScoredDoc{{DocID: 10, Score: 5}, {DocID: 20, Score: 1}})
		partial[0].Score = -999 // Result ownership must not disturb later heapify.

		So(c.Add(30), ShouldEqual, CollectContinue)
		threshold, full := c.Threshold()
		So(full, ShouldBeTrue)
		So(threshold, ShouldResemble, ScoredDoc{DocID: 20, Score: 1})
		So(c.NextCandidate(entry), ShouldEqual, entry)
		So(scores.calls, ShouldEqual, 1)
		So(scores.threshold, ShouldResemble, threshold)

		So(c.Add(40), ShouldEqual, CollectContinue)
		So(c.Add(-10), ShouldEqual, CollectContinue) // Same score as 30; smaller signed ID wins.
		So(c.Add(20), ShouldEqual, CollectContinue)  // Evicted result must not displace a winner.
		So(c.Scored(), ShouldResemble, []ScoredDoc{{DocID: 40, Score: 8}, {DocID: 10, Score: 5}, {DocID: -10, Score: 3}})
		threshold, full = c.Threshold()
		So(full, ShouldBeTrue)
		So(threshold, ShouldResemble, ScoredDoc{DocID: -10, Score: 3})

		c.Reset(scores)
		c.Add(30)
		c.Add(20)
		_, full = c.Threshold()
		So(full, ShouldBeFalse)
		c.NextCandidate(entry)
		So(scores.calls, ShouldEqual, 1)
		So(c.Scored(), ShouldResemble, []ScoredDoc{{DocID: 30, Score: 3}, {DocID: 20, Score: 1}})
		c.Add(10)
		threshold, full = c.Threshold()
		So(full, ShouldBeTrue)
		So(threshold, ShouldResemble, ScoredDoc{DocID: 20, Score: 1})
		c.Add(40)
		So(c.Scored(), ShouldResemble, []ScoredDoc{{DocID: 40, Score: 8}, {DocID: 10, Score: 5}, {DocID: 30, Score: 3}})
	})

	Convey("limits zero and one preserve stop and threshold contracts", t, func() {
		src := mapScoreSource{1: 1, -1: 1}
		zero := NewTopKCollector(0, src)
		So(zero.Add(1), ShouldEqual, CollectStop)
		_, full := zero.Threshold()
		So(full, ShouldBeFalse)
		one := NewTopKCollector(1, src)
		So(one.Add(1), ShouldEqual, CollectContinue)
		threshold, full := one.Threshold()
		So(full, ShouldBeTrue)
		So(threshold, ShouldResemble, ScoredDoc{DocID: 1, Score: 1})
		one.Add(-1)
		So(one.Scored(), ShouldResemble, []ScoredDoc{{DocID: -1, Score: 1}})
	})
}

func TestScoreTableSignedAndOwnership(t *testing.T) {
	Convey("score snapshots own inputs and roaring ranks signed IDs correctly", t, func() {
		ids := []DocID{-10, -1, 0, 3}
		values := []float64{10, 1, 0, 3}
		roaring, err := NewRoaringScoreTable(ids, values)
		So(err, ShouldBeNil)
		blocks, err := NewBlockMaxScoreTable(ids, values)
		So(err, ShouldBeNil)
		dense := NewDenseScoreTable(-10, values)
		ids[0] = 100
		values[0] = 999
		for _, src := range []ScoreSource{roaring, blocks, dense} {
			v, ok := src.Score(-10)
			So(ok, ShouldBeTrue)
			So(v, ShouldEqual, 10)
		}
		for _, src := range []ScoreSource{roaring, blocks} {
			for _, id := range []DocID{-1, 0, 3} {
				v, ok := src.Score(id)
				So(ok, ShouldBeTrue)
				So(v, ShouldEqual, math.Abs(float64(id)))
			}
		}
		_, err = NewBlockMaxScoreTable([]DocID{1, 1}, []float64{1, 2})
		So(err, ShouldNotBeNil)
		_, err = NewBlockMaxScoreTable([]DocID{MaxDocID + 1}, []float64{1})
		So(err, ShouldNotBeNil)
		_, err = NewBlockMaxScoreTable([]DocID{1}, nil)
		So(err, ShouldNotBeNil)
	})
}

func TestBlockMaxCertifiedSkips(t *testing.T) {
	Convey("every skipped interval is safe across K, index, signs, ties and absent scores", t, func() {
		rng := rand.New(rand.NewSource(819))
		ids := []DocID{-MaxDocID}
		values := []float64{2}
		for id := DocID(-150); id <= 150; id++ {
			if rng.Intn(4) != 0 {
				ids = append(ids, id)
				values = append(values, float64(rng.Intn(9)-4))
			}
		}
		ids = append(ids, MaxDocID)
		values = append(values, 3)
		values[10] = math.NaN()
		values[20] = math.Inf(1)
		values[30] = math.Inf(-1)
		table, err := NewBlockMaxScoreTable(ids, values)
		So(err, ShouldBeNil)
		// Include unscored IDs and all the prefix boundary cases in a sorted
		// synthetic posting. This oracle never consults the max-tree implementation.
		entries := Entries{}
		for _, k := range []int{0, 1, 2, 254, 255} {
			for _, idx := range []int{0, 1, 254, 255} {
				for id := DocID(-151); id <= 151; id++ {
					for _, inc := range []bool{false, true} {
						entries = append(entries, NewEntryID(NewConjID(id, idx, k), inc))
					}
				}
				entries = append(entries, NewEntryID(NewConjID(-MaxDocID, idx, k), true), NewEntryID(NewConjID(MaxDocID, idx, k), true))
			}
		}
		sort.Sort(entries)
		for trial := 0; trial < 1200; trial++ {
			pos := rng.Intn(len(entries))
			entry := entries[pos]
			threshold := ScoredDoc{DocID: DocID(rng.Intn(303) - 151), Score: float64(rng.Intn(13) - 6)}
			if trial%10 == 0 {
				threshold.Score = math.Inf(1)
			}
			for _, mode := range []MissingScoreMode{MissingScoreZero, MissingScoreSkip} {
				target := table.NextCompetitive(entry, threshold, mode)
				if target < entry {
					t.Fatalf("backward target %x < %x", target, entry)
				}
				for i := pos; i < len(entries) && entries[i] < target; i++ {
					id := entries[i].ConjID().DocID()
					score, ok := table.Score(id)
					if !ok {
						if mode == MissingScoreSkip {
							continue
						}
						score = 0
					}
					if score > threshold.Score || (score == threshold.Score && id < threshold.DocID) {
						t.Fatalf("unsafe skip entry=%x target=%x doc=%d score=%v threshold=%v mode=%v", entry, target, id, score, threshold, mode)
					}
				}
			}
		}
		So(table.NextCompetitive(EndEntryID, ScoredDoc{}, MissingScoreSkip), ShouldEqual, EndEntryID)
		// No allocation in the sidecar bound lookup, including prefix carry.
		So(testing.AllocsPerRun(100, func() { table.NextCompetitive(entries[len(entries)-2], ScoredDoc{Score: 1}, MissingScoreSkip) }), ShouldEqual, 0)
	})
}
