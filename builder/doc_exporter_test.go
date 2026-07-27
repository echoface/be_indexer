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
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault, Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault}},
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
		s1 := bitmapToSlice(r1)
		s2 := bitmapToSlice(r2)
		sort.Slice(s1, func(i, j int) bool { return s1[i] < s1[j] })
		sort.Slice(s2, func(i, j int) bool { return s2[i] < s2[j] })
		if len(s1) != len(s2) {
			t.Fatalf("result len mismatch for q[%d]=%v: %v vs %v", i, q, s1, s2)
		}
		for j := range s1 {
			if s1[j] != s2[j] {
				t.Fatalf("result mismatch for q[%d]=%v: %v vs %v", i, q, s1, s2)
			}
		}
	}
}

// TestBuildSegmentsFromDocs_EmbedsPerSegmentWildcards ensures the multi-segment
// helper writes a non-empty __wildcards block into each segment that has K=0
// conjunctions. Loader recovery reads seg.Wildcards() only; it must not depend
// on the returned union entries alone.
func TestBuildSegmentsFromDocs_EmbedsPerSegmentWildcards(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault, Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault}},
	}
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 18)),
		// K=0 pure exclude → must produce a per-segment wildcard EntryID
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("city", "bj")),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("age", 20).In("city", "sh")),
	}

	var segBuffers []*bytes.Buffer
	returnedWildcards, segCnt, err := BuildSegmentsFromDocs(func(segIdx int) (io.Writer, error) {
		b := new(bytes.Buffer)
		segBuffers = append(segBuffers, b)
		return b, nil
	}, fields, docs, BuildSegmentsFromDocsOptions{MaxDocsPerSegment: 1})
	if err != nil {
		t.Fatalf("BuildSegmentsFromDocs failed: %v", err)
	}
	if segCnt != 3 {
		t.Fatalf("want 3 segments, got %d", segCnt)
	}

	// Reconstruct wildcards the same way loader.embeddedWildcards does.
	var embedded core.Entries
	segs := make([]*segment.SegmentReader, 0, len(segBuffers))
	for i, b := range segBuffers {
		sr, err := segment.NewSegmentReader(b.Bytes())
		if err != nil {
			t.Fatalf("NewSegmentReader[%d] failed: %v", i, err)
		}
		segs = append(segs, sr)
		embedded = append(embedded, sr.Wildcards()...)
	}
	sort.Slice(embedded, func(i, j int) bool { return embedded[i] < embedded[j] })

	if len(embedded) == 0 {
		t.Fatal("embedded per-segment wildcards is empty; multi-segment path forgot SetWildcards")
	}
	if len(embedded) != len(returnedWildcards) {
		t.Fatalf("embedded wildcards len=%d, returned union len=%d", len(embedded), len(returnedWildcards))
	}
	for i := range embedded {
		if embedded[i] != returnedWildcards[i] {
			t.Fatalf("embedded wildcards mismatch returned union at %d: %v vs %v", i, embedded, returnedWildcards)
		}
	}
	// Only doc2 (segment index 1) is K=0.
	if got := segs[1].Wildcards(); len(got) != 1 {
		t.Fatalf("segment[1] (K=0 doc) wildcards want 1, got %v", got)
	}
	if got := segs[0].Wildcards(); len(got) != 0 {
		t.Fatalf("segment[0] (K=1 doc) wildcards want empty, got %v", got)
	}
	if got := segs[2].Wildcards(); len(got) != 0 {
		t.Fatalf("segment[2] (K=2 doc) wildcards want empty, got %v", got)
	}

	// Engine built only from embedded wildcards must still hit the K=0 doc.
	eng, err := engine.NewBooleanEngine(fields, embedded, segs)
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}
	// city=sh does not match exclude city=bj, so K=0 doc2 should hit.
	res, err := eng.Retrieve(core.Assignments{"city": "sh"})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	ids := bitmapToSlice(res)
	found := false
	for _, id := range ids {
		if id == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("K=0 doc 2 missing from result %v when engine uses only embedded wildcards", ids)
	}
}

func bitmapToSlice(b *core.BitmapDocSet) core.DocIDList {
	var ids core.DocIDList
	b.ForEach(func(id core.DocID) { ids = append(ids, id) })
	return ids
}
