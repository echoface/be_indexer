package builder

import (
	"bytes"
	"io"
	"math/rand"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

// TestBuildSegmentsFromDocs_ShadowTest verifies multi-segment consistency
// against a single-segment baseline. Generated documents and queries are
// randomized for broader coverage.
func TestBuildSegmentsFromDocs_ShadowTest(t *testing.T) {
	const numDocs = 100
	const numQueries = 500
	const seed = 42

	rng := rand.New(rand.NewSource(seed))

	fields := map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{}},
		"tag":  {ID: 3, Field: "tag", FieldOption: core.FieldOption{}},
	}

	cities := []string{"bj", "sh", "gz", "sz", "cd", "hz"}
	tags := []string{"vip", "new", "active", "premium"}

	docs := make([]*core.Document, numDocs)
	for i := 0; i < numDocs; i++ {
		doc := core.NewDocument(core.DocID(i + 1))
		numConj := rng.Intn(3) + 1
		for j := 0; j < numConj; j++ {
			conj := core.NewConjunction()
			if rng.Intn(2) == 0 {
				age := rng.Intn(80) + 10
				if rng.Intn(4) == 0 {
					conj.Exclude("age", age)
				} else {
					conj.Include("age", age)
				}
			}
			if rng.Intn(2) == 0 {
				city := cities[rng.Intn(len(cities))]
				if rng.Intn(4) == 0 {
					conj.NotIn("city", city)
				} else {
					conj.In("city", city)
				}
			}
			if rng.Intn(3) == 0 {
				tag := tags[rng.Intn(len(tags))]
				if rng.Intn(4) == 0 {
					conj.NotIn("tag", tag)
				} else {
					conj.In("tag", tag)
				}
			}
			doc.AddConjunction(conj)
		}
		if doc.Cons == nil || len(doc.Cons) == 0 {
			// Ensure at least one conjunction (K=0, always match)
			doc.AddConjunction(core.NewConjunction())
		}
		docs[i] = doc
	}

	// Baseline: single segment
	buf := new(bytes.Buffer)
	baselineWildcards, err := BuildSegmentFromDocs(buf, fields, docs)
	if err != nil {
		t.Fatalf("baseline build failed: %v", err)
	}
	baselineSeg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("baseline reader failed: %v", err)
	}
	baselineEngine, err := engine.NewBooleanEngine(fields, baselineWildcards, []*segment.SegmentReader{baselineSeg})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}

	for _, cfg := range []struct {
		name      string
		chunkSize int
	}{
		{"chunk=1", 1},
		{"chunk=5", 5},
		{"chunk=20", 20},
		{"single-chunk", numDocs + 1}, // all in one chunk using BuildSegmentsFromDocs
	} {
		t.Run(cfg.name, func(t *testing.T) {
			var segBuffers []*bytes.Buffer
			wildcards, _, err := BuildSegmentsFromDocs(
				func(segIdx int) (io.Writer, error) {
					b := new(bytes.Buffer)
					segBuffers = append(segBuffers, b)
					return b, nil
				},
				fields, docs,
				BuildSegmentsFromDocsOptions{MaxDocsPerSegment: cfg.chunkSize},
			)
			if err != nil {
				t.Fatalf("multi-segment build failed: %v", err)
			}

			segs := make([]*segment.SegmentReader, 0, len(segBuffers))
			for _, b := range segBuffers {
				sr, err := segment.NewSegmentReader(b.Bytes())
				if err != nil {
					t.Fatalf("reader failed: %v", err)
				}
				segs = append(segs, sr)
			}
			multiEngine, err := engine.NewBooleanEngine(fields, wildcards, segs)
			if err != nil {
				t.Fatalf("NewBooleanEngine failed: %v", err)
			}

			for qi := 0; qi < numQueries; qi++ {
				q := core.Assignments{}
				if rng.Intn(2) == 0 {
					q["age"] = rng.Intn(80) + 10
				}
				if rng.Intn(2) == 0 {
					q["city"] = cities[rng.Intn(len(cities))]
				}
				if rng.Intn(3) == 0 {
					q["tag"] = tags[rng.Intn(len(tags))]
				}

				r1, err := baselineEngine.Retrieve(q)
				if err != nil {
					t.Fatalf("baseline Retrieve[%d] failed: %v", qi, err)
				}
				r2, err := multiEngine.Retrieve(q)
				if err != nil {
					t.Fatalf("multi Retrieve[%d] failed: %v", qi, err)
				}

				s1 := bitmapToSlice(r1)
				s2 := bitmapToSlice(r2)
				sort.Slice(s1, func(i, j int) bool { return s1[i] < s1[j] })
				sort.Slice(s2, func(i, j int) bool { return s2[i] < s2[j] })

				if len(s1) != len(s2) {
					t.Fatalf("[%s] q[%d]=%v len mismatch: %d vs %d\n  baseline=%v\n  multi   =%v",
						cfg.name, qi, q, len(s1), len(s2), s1, s2)
				}
				for j := range s1 {
					if s1[j] != s2[j] {
						t.Fatalf("[%s] q[%d]=%v: result[%d] mismatch: %d vs %d\n  baseline=%v\n  multi   =%v",
							cfg.name, qi, q, j, s1[j], s2[j], s1, s2)
					}
				}
			}
		})
	}
}
