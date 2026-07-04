package parser

import (
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestExactTermEncoderUsesTokenizerInBothDirections(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "age",
		FieldOption: core.FieldOption{Container: core.IndexNameDefault, Tokenizer: "number"},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptEQ, Value: []interface{}{"18", 18.9}})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 1 || postings[0].Kind != PostingKindTerm || postings[0].Term != "18" {
		t.Fatalf("unexpected build postings: %#v", postings)
	}
	queries, err := enc.Query("18")
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Kind != QueryKindTerm || queries[0].Term != "18" {
		t.Fatalf("unexpected query keys: %#v", queries)
	}
}

func TestRangeEncoderBuildAndQuery(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "age",
		FieldOption: core.FieldOption{Container: core.IndexNameExtendRange},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptGT, Value: 18})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 1 || postings[0].Kind != PostingKindRange || postings[0].Lo != 19 {
		t.Fatalf("unexpected range postings: %#v", postings)
	}
	queries, err := enc.Query(25)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Kind != QueryKindRange || queries[0].Point != 25 {
		t.Fatalf("unexpected range query: %#v", queries)
	}
}

func TestACEncoderBuildAndQuery(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "keyword",
		FieldOption: core.FieldOption{Container: core.IndexNameACMatcher},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptEQ, Value: []string{"apple", "banana"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 || postings[0].Kind != PostingKindAC || postings[0].Term != "apple" || postings[1].Term != "banana" {
		t.Fatalf("unexpected ac postings: %#v", postings)
	}
	queries, err := enc.Query([]string{"I love", "apple"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Kind != QueryKindAC || queries[0].Text != "I love apple" {
		t.Fatalf("unexpected ac query: %#v", queries)
	}
}
