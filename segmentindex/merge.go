package segmentindex

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/storage"
)

// groupSignature encodes the conjunction metadata that must match for two
// groups to coalesce: per-field expression shapes and the full range predicate
// set (bounds included). Exact term values are not part of the signature:
// they live in postings and merge freely once the shapes match. Groups sharing
// any doc id never coalesce (see mergeDeltas).
func groupSignature(meta groupMeta) string {
	fields := make([]string, 0, len(meta.shapes))
	for field := range meta.shapes {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	var b strings.Builder
	for _, field := range fields {
		shape := meta.shapes[field]
		fmt.Fprintf(&b, "f:%s:%t:%t;", field, shape.hasIncl, shape.hasExcl)
	}
	ranges := append([]rangePredicate(nil), meta.ranges...)
	sort.Slice(ranges, func(i, j int) bool {
		a, c := ranges[i], ranges[j]
		if a.field != c.field {
			return a.field < c.field
		}
		if a.left != c.left {
			return a.left < c.left
		}
		if a.right != c.right {
			return a.right < c.right
		}
		return !a.incl && c.incl
	})
	for _, r := range ranges {
		fmt.Fprintf(&b, "r:%s:%t:%d:%d;", r.field, r.incl, r.left, r.right)
	}
	return b.String()
}

// mergeBucket accumulates one output group during a merge.
type mergeBucket struct {
	shapes  map[string]fieldShape
	ranges  []rangePredicate
	seqs    map[uint64]uint64
	builder *storage.SegmentBuilder
}

// mergeDeltas rewrites the given deltas into one Index. Tombstoned doc
// versions are stripped from every posting and live set, groups without
// survivors disappear, and same-signature groups whose doc id sets are
// disjoint coalesce into one segment, merging their Field/Term postings and
// rebuilding stats (live docs, counts) on seal.
//
// Disjointness is required for correctness: one doc may own several
// conjunctions, and two versions of one doc may live in different deltas.
// Keeping such groups apart preserves per-conjunction DNF boundaries and lets
// tombstone checks see the right add sequence per version.
//
// The limiter paces the rewrite (cost unit: processed doc versions) so a
// background merge cannot starve online queries. A limiter or context failure
// aborts the merge: nothing has been published, so the online version stays
// unchanged and the partial buckets are garbage collected.
func mergeDeltas(ctx context.Context, deltas []*Delta, tomb *tombstoneSet, limiter RateLimiter) (*Index, error) {
	bySig := make(map[string][]*mergeBucket)
	sigs := make([]string, 0)
	for _, delta := range deltas {
		for idx, meta := range delta.index.groups {
			segment := delta.index.segments[idx]
			seqs := make(map[uint64]uint64, len(meta.seqs))
			for id, seq := range meta.seqs {
				if !meta.dead(id, tomb) {
					seqs[id] = seq
				}
			}
			if len(seqs) == 0 {
				continue // every version of this group is tombstoned
			}
			keys := segment.TermKeys()
			terms := make([]storage.TermKey, 0, len(keys))
			postings := make([][]uint64, 0, len(keys))
			cost := len(seqs)
			for termID, key := range keys {
				ids, err := segment.Posting(storage.TermID(termID))
				if err != nil {
					return nil, fmt.Errorf("merge delta %d: %w", delta.id, err)
				}
				kept := make([]uint64, 0, len(ids))
				for _, id := range ids {
					if _, live := seqs[id]; live {
						kept = append(kept, id)
					}
				}
				if len(kept) == 0 {
					continue // term lost every doc to tombstones
				}
				terms = append(terms, key)
				postings = append(postings, kept)
				cost += len(kept)
			}

			sig := groupSignature(meta)
			if _, seen := bySig[sig]; !seen {
				sigs = append(sigs, sig)
			}
			bucket := pickBucket(bySig[sig], seqs)
			if bucket == nil {
				bucket = &mergeBucket{
					shapes:  copyShapes(meta.shapes),
					ranges:  append([]rangePredicate(nil), meta.ranges...),
					seqs:    make(map[uint64]uint64, len(seqs)),
					builder: storage.NewSegmentBuilder(),
				}
				bySig[sig] = append(bySig[sig], bucket)
			}
			for id, seq := range seqs {
				bucket.seqs[id] = seq
				if err := bucket.builder.AddLiveDoc(id); err != nil {
					return nil, err
				}
			}
			for i := range terms {
				if err := bucket.builder.AddPosting(terms[i].Field, terms[i].Value, postings[i]); err != nil {
					return nil, err
				}
			}
			if limiter != nil {
				if err := limiter.WaitN(ctx, cost); err != nil {
					return nil, err
				}
			}
		}
	}

	segments := make([]*storage.Segment, 0, len(sigs))
	groups := make([]groupMeta, 0, len(sigs))
	for _, sig := range sigs {
		for _, bucket := range bySig[sig] {
			segment, err := bucket.builder.Seal()
			if err != nil {
				return nil, err
			}
			docs := make([]uint64, 0, len(bucket.seqs))
			for id := range bucket.seqs {
				docs = append(docs, id)
			}
			sort.Slice(docs, func(i, j int) bool { return docs[i] < docs[j] })
			segments = append(segments, segment)
			groups = append(groups, groupMeta{
				shapes: bucket.shapes,
				ranges: bucket.ranges,
				seqs:   bucket.seqs,
				docs:   docs,
			})
		}
	}
	return newIndex(segments, groups), nil
}

// pickBucket returns the first bucket with the same signature whose doc id
// set is disjoint from seqs, or nil when the group must stay on its own.
func pickBucket(buckets []*mergeBucket, seqs map[uint64]uint64) *mergeBucket {
	for _, bucket := range buckets {
		if disjoint(bucket.seqs, seqs) {
			return bucket
		}
	}
	return nil
}

func disjoint(a, b map[uint64]uint64) bool {
	small, large := a, b
	if len(small) > len(large) {
		small, large = large, small
	}
	for id := range small {
		if _, ok := large[id]; ok {
			return false
		}
	}
	return true
}

func copyShapes(shapes map[string]fieldShape) map[string]fieldShape {
	out := make(map[string]fieldShape, len(shapes))
	for field, shape := range shapes {
		out[field] = shape
	}
	return out
}
