package planner

import (
	"reflect"
	"testing"

	"github.com/echoface/be_indexer/storage"
)

func testSegment(t *testing.T) *storage.Segment {
	t.Helper()
	builder := storage.NewSegmentBuilder()
	postings := map[string][]uint64{
		"a=rare":   {1, 3},
		"a=common": {1, 2, 3, 4, 5, 6},
		"b=x":      {1, 2, 3},
		"b=y":      {4, 5},
		"c=deny":   {2, 5},
	}
	for key, ids := range postings {
		field, value := key[:1], key[2:]
		if err := builder.AddPosting(field, value, ids); err != nil {
			t.Fatal(err)
		}
	}
	segment, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return segment
}

func TestPlannerOrdersByCardinalityAndSelectsStrategy(t *testing.T) {
	segment := testSegment(t)
	planner := New()
	planner.BitmapThreshold = 4
	plan, err := planner.Plan(segment, Query{Terms: []TermQuery{
		{Field: "a", Value: "common", Op: OpAND},
		{Field: "a", Value: "rare", Op: OpAND},
		{Field: "c", Value: "deny", Op: OpNOT},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Terms) != 3 {
		t.Fatalf("term plan count=%d", len(plan.Terms))
	}
	if plan.Terms[0].Cardinality != 2 || plan.Terms[1].Query.Op != OpAND || plan.Terms[2].Query.Op != OpNOT {
		t.Fatalf("unexpected term order: %+v", plan.Terms)
	}
	if plan.Terms[1].Strategy != StrategyBitmap {
		t.Fatalf("common term strategy=%s want bitmap", plan.Terms[1].Strategy)
	}
}

func TestPlannerSkipsSegmentForMissingPositiveTerm(t *testing.T) {
	plan, err := New().Plan(testSegment(t), Query{Terms: []TermQuery{{Field: "missing", Value: "x", Op: OpAND}}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SkippedSegment || len(plan.Terms) != 0 {
		t.Fatalf("unexpected skipped plan: %+v", plan)
	}
}

func TestExecuteCursorAndBitmap(t *testing.T) {
	segment := testSegment(t)
	cases := []struct {
		name      string
		threshold uint32
		query     Query
		want      []uint64
	}{
		{
			name: "and not sparse", threshold: 100,
			query: Query{Terms: []TermQuery{
				{Field: "a", Value: "rare", Op: OpAND},
				{Field: "c", Value: "deny", Op: OpNOT},
			}}, want: []uint64{1, 3},
		},
		{
			name: "and bitmap", threshold: 1,
			query: Query{Terms: []TermQuery{
				{Field: "a", Value: "common", Op: OpAND},
				{Field: "b", Value: "x", Op: OpAND},
			}}, want: []uint64{1, 2, 3},
		},
		{
			name: "or", threshold: 100,
			query: Query{Terms: []TermQuery{
				{Field: "b", Value: "x", Op: OpOR},
				{Field: "b", Value: "y", Op: OpOR},
			}}, want: []uint64{1, 2, 3, 4, 5},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := (&Planner{BitmapThreshold: tc.threshold}).Plan(segment, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Execute(segment, plan)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v plan=%+v", got, tc.want, plan)
			}
		})
	}
}

func TestExplainJSON(t *testing.T) {
	segment := testSegment(t)
	plan, err := New().Plan(segment, Query{Terms: []TermQuery{{Field: "a", Value: "rare", Op: OpAND}}})
	if err != nil {
		t.Fatal(err)
	}
	json, err := Explain(segment, plan).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(json) == 0 {
		t.Fatal("empty explain JSON")
	}
}
