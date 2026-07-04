package oracle_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/oracle"
	"github.com/echoface/be_indexer/segment"
)

func oracleFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}},
		"b": {ID: 2, Field: "b", FieldOption: core.FieldOption{Tokenizer: "number"}},
	}
}

func buildOracleEngine(t *testing.T, docs []*core.Document) *engine.BooleanEngine {
	t.Helper()
	buf := new(bytes.Buffer)
	wildcards, err := builder.BuildSegmentFromDocs(buf, oracleFields(), docs)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.NewBooleanEngine(oracleFields(), wildcards, []*segment.SegmentReader{reader})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}
	return eng
}

func TestOracleExcludeOnPresentValue(t *testing.T) {
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1).NotIn("b", 2)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("b", 2)),
	}
	cases := []struct {
		name string
		q    core.Assignments
		want core.DocIDList
	}{
		{name: "missing exclude field", q: core.Assignments{"a": 1}, want: core.DocIDList{1, 2}},
		{name: "exclude field not hit", q: core.Assignments{"a": 1, "b": 3}, want: core.DocIDList{1, 2}},
		{name: "exclude field hit", q: core.Assignments{"a": 1, "b": 2}, want: nil},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := oracle.MatchDocuments(docs, tt.q)
			if err != nil {
				t.Fatal(err)
			}
			assertDocIDs(t, got, tt.want)
		})
	}
}

func TestCompositeEngineMatchesOracleAfterMutationReplay(t *testing.T) {
	fullDocs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1).NotIn("b", 9)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 3)),
	}
	mutations := []builder.Mutation{
		// doc1 updated from a=1 to a=2.
		{DocID: 1, Version: 2, Op: builder.MutationUpsert, Document: core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2))},
		// doc2 deleted.
		{DocID: 2, Version: 2, Op: builder.MutationDelete},
		// doc4 created then updated; latest should be a=4,b!=8.
		{DocID: 4, Version: 1, Op: builder.MutationUpsert, Document: core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 1))},
		{DocID: 4, Version: 3, Op: builder.MutationUpsert, Document: core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 4).NotIn("b", 8))},
	}
	plan, err := builder.BuildDeltaPlan(mutations)
	if err != nil {
		t.Fatal(err)
	}
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:  buildOracleEngine(t, fullDocs),
		DeltaEngine: buildOracleEngine(t, plan.Documents),
		ChangedDocs: engine.NewBitmapDocSet(plan.ChangedDocs...),
		DeletedDocs: engine.NewBitmapDocSet(plan.DeletedDocs...),
	})

	latestDocs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 3)),
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 4).NotIn("b", 8)),
	}
	queries := []core.Assignments{
		{"a": 1},
		{"a": 2},
		{"a": 3},
		{"a": 4},
		{"a": 4, "b": 8},
		{"a": 4, "b": 7},
	}
	for _, q := range queries {
		want, err := oracle.MatchDocuments(latestDocs, q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ce.Retrieve(q)
		if err != nil {
			t.Fatal(err)
		}
		assertDocIDs(t, got, want)
	}
}

func assertDocIDs(t *testing.T, got, want core.DocIDList) {
	t.Helper()
	sort.Sort(got)
	sort.Sort(want)
	if len(got) != len(want) {
		t.Fatalf("ids length mismatch: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids mismatch: got=%v want=%v", got, want)
		}
	}
}
