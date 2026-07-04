package builder

import (
	"bytes"
	"io"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

func TestBuildSegmentsFromDocs_Consistency(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "default"}},
	}

	// docs:
	// - doc1: K=1 include age=18
	// - doc2: K=0 exclude city=bj (pure exclude conj) + wildcard include for K=0
	// - doc3: K=2 include age=20 and city=sh
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 18)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("city", "bj")),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("age", 20).In("city", "sh")),
	}

	// Build single segment
	buf := new(bytes.Buffer)
	wild1, err := BuildSegmentFromDocs(buf, fields, docs)
	if err != nil {
		t.Fatalf("BuildSegmentFromDocs failed: %v", err)
	}
	seg1, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("NewSegmentReader failed: %v", err)
	}
	eng1, err := engine.NewBooleanEngine(fields, wild1, []*segment.SegmentReader{seg1})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}

	// Build multi segments (chunk=1)
	var segBuffers []*bytes.Buffer
	wild2, segCnt, err := BuildSegmentsFromDocs(func(segIdx int) (io.Writer, error) {
		b := new(bytes.Buffer)
		segBuffers = append(segBuffers, b)
		return b, nil
	}, fields, docs, BuildSegmentsFromDocsOptions{MaxDocsPerSegment: 1})
	if err != nil {
		t.Fatalf("BuildSegmentsFromDocs failed: %v", err)
	}
	if segCnt != len(segBuffers) {
		t.Fatalf("segment count mismatch: ret=%d actual=%d", segCnt, len(segBuffers))
	}

	segs := make([]*segment.SegmentReader, 0, len(segBuffers))
	for i, b := range segBuffers {
		sr, err := segment.NewSegmentReader(b.Bytes())
		if err != nil {
			t.Fatalf("NewSegmentReader[%d] failed: %v", i, err)
		}
		segs = append(segs, sr)
	}
	eng2, err := engine.NewBooleanEngine(fields, wild2, segs)
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}

	// Compare results for a few representative queries
	queries := []core.Assignments{
		{"age": 18},
		{"city": "bj"},
		{"age": 20, "city": "sh"},
		{"city": "sh"},
	}
	for i, q := range queries {
		r1, err := eng1.Retrieve(q)
		if err != nil {
			t.Fatalf("eng1.Retrieve[%d] failed: %v", i, err)
		}
		r2, err := eng2.Retrieve(q)
		if err != nil {
			t.Fatalf("eng2.Retrieve[%d] failed: %v", i, err)
		}
		sort.Slice(r1, func(i, j int) bool { return r1[i] < r1[j] })
		sort.Slice(r2, func(i, j int) bool { return r2[i] < r2[j] })
		if len(r1) != len(r2) {
			t.Fatalf("result len mismatch for q[%d]=%v: %v vs %v", i, q, r1, r2)
		}
		for j := range r1 {
			if r1[j] != r2[j] {
				t.Fatalf("result mismatch for q[%d]=%v: %v vs %v", i, q, r1, r2)
			}
		}
	}
}
