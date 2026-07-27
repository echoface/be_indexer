package parser

import (
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestExactTermEncoderUsesTokenizerInBothDirections(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "age",
		FieldOption: core.FieldOption{IndexType: core.IndexNameDefault, Encoder: "number"},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptEQ, Value: []interface{}{"18", 18.9}})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 1 || postings[0].Record != "18" {
		t.Fatalf("unexpected build postings: %#v", postings)
	}
	queries, err := enc.Query("18")
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Value != "18" {
		t.Fatalf("unexpected query keys: %#v", queries)
	}
}

func TestRangeEncoderBuildAndQuery(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "age",
		FieldOption: core.FieldOption{IndexType: core.IndexNameExtendRange, Encoder: core.IndexNameExtendRange},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptGT, Value: 18})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 1 {
		t.Fatalf("unexpected range postings: %#v", postings)
	}
	rr, ok := postings[0].Record.(core.RangeRecord)
	if !ok || rr.Lo != 19 {
		t.Fatalf("unexpected range postings: %#v", postings)
	}
	queries, err := enc.Query(25)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Value != int64(25) {
		t.Fatalf("unexpected range query: %#v", queries)
	}
}

func TestACEncoderBuildAndQuery(t *testing.T) {
	enc, err := NewPredicateEncoder(core.FieldMeta{
		Field:       "keyword",
		FieldOption: core.FieldOption{IndexType: core.IndexNameACMatcher, Encoder: core.IndexNameACMatcher},
	})
	if err != nil {
		t.Fatal(err)
	}
	postings, err := enc.Build(&core.ValueExpr{Incl: true, Operator: core.ValueOptEQ, Value: []string{"apple", "banana"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) != 2 || postings[0].Record != "apple" || postings[1].Record != "banana" {
		t.Fatalf("unexpected ac postings: %#v", postings)
	}
	queries, err := enc.Query([]string{"I love", "apple"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Value != "I love apple" {
		t.Fatalf("unexpected ac query: %#v", queries)
	}
}
