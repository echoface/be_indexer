package planner

import (
	"sort"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer/storage"
)

// Execute runs a plan against an immutable Segment and returns sorted DocIDs.
// It uses cursor operations for sparse plans and roaring bitmaps when the plan
// contains a dense posting or the selected strategy requires bitmap execution.
func Execute(segment *storage.Segment, plan Plan) ([]uint64, error) {
	if plan.SkippedSegment {
		return nil, nil
	}
	positive := make([][]uint64, 0)
	orTerms := make([][]uint64, 0)
	notTerms := make([][]uint64, 0)
	useBitmap := false
	for _, term := range plan.Terms {
		posting, err := segment.Posting(term.TermID)
		if err != nil {
			return nil, err
		}
		if term.Strategy == StrategyBitmap {
			useBitmap = true
		}
		switch term.Query.Op {
		case OpNOT:
			notTerms = append(notTerms, posting)
		case OpOR:
			orTerms = append(orTerms, posting)
		default:
			positive = append(positive, posting)
		}
	}
	if useBitmap {
		return executeBitmapWithUniverse(segment, positive, orTerms, notTerms)
	}
	return executeCursorWithUniverse(segment, positive, orTerms, notTerms)
}

func executeCursorWithUniverse(segment *storage.Segment, positive, orTerms, notTerms [][]uint64) ([]uint64, error) {
	var result []uint64
	if len(orTerms) > 0 {
		result = unionSorted(orTerms...)
	}
	for _, posting := range positive {
		if result == nil {
			result = append([]uint64(nil), posting...)
		} else {
			result = intersectSorted(result, posting)
		}
	}
	if result == nil {
		// A query containing only NOT needs a universe. The Segment API will add
		// explicit live docs in a later integration step; an empty candidate is
		// the safe result for this minimal exact-term executor.
		var err error
		result, err = segment.AllDocIDs()
		if err != nil {
			return nil, err
		}
	}
	for _, posting := range notTerms {
		result = differenceSorted(result, posting)
	}
	return result, nil
}

func executeBitmapWithUniverse(segment *storage.Segment, positive, orTerms, notTerms [][]uint64) ([]uint64, error) {
	var result *roaring64.Bitmap
	if len(orTerms) > 0 {
		result = roaring64.New()
		for _, posting := range orTerms {
			addBitmap(result, posting)
		}
	}
	for _, posting := range positive {
		current := roaring64.New()
		addBitmap(current, posting)
		if result == nil {
			result = current
		} else {
			result.And(current)
		}
	}
	if result == nil {
		universe, err := segment.AllDocIDs()
		if err != nil {
			return nil, err
		}
		result = roaring64.New()
		addBitmap(result, universe)
	}
	for _, posting := range notTerms {
		current := roaring64.New()
		addBitmap(current, posting)
		result.AndNot(current)
	}
	ids := make([]uint64, 0, result.GetCardinality())
	iterator := result.Iterator()
	for iterator.HasNext() {
		ids = append(ids, iterator.Next())
	}
	return ids, nil
}

func addBitmap(bitmap *roaring64.Bitmap, posting []uint64) {
	for _, id := range posting {
		bitmap.Add(id)
	}
}

func unionSorted(postings ...[]uint64) []uint64 {
	seen := make(map[uint64]struct{})
	for _, posting := range postings {
		for _, id := range posting {
			seen[id] = struct{}{}
		}
	}
	result := make([]uint64, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func intersectSorted(left, right []uint64) []uint64 {
	result := make([]uint64, 0)
	i, j := 0, 0
	for i < len(left) && j < len(right) {
		switch {
		case left[i] < right[j]:
			i++
		case left[i] > right[j]:
			j++
		default:
			result = append(result, left[i])
			i++
			j++
		}
	}
	return result
}

func differenceSorted(left, right []uint64) []uint64 {
	result := make([]uint64, 0, len(left))
	j := 0
	for _, id := range left {
		for j < len(right) && right[j] < id {
			j++
		}
		if j >= len(right) || right[j] != id {
			result = append(result, id)
		}
	}
	return result
}
