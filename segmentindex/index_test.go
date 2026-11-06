package segmentindex

import (
	"errors"
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
)

func TestBuilderSealAndRetrieve(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("ad"))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("organic"))),
		be.NewDocument(3).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("shanghai")).In("kind", be.NewStrValues("ad"))),
	}
	if err := builder.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	query := planner.Query{Terms: []planner.TermQuery{
		{Field: "city", Value: "beijing", Op: planner.OpAND},
		{Field: "kind", Value: "ad", Op: planner.OpAND},
	}}
	got, err := index.Retrieve(query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{1}) {
		t.Fatalf("got %v want [1]", got)
	}
}

func TestBuilderPreservesConjunctionORBoundary(t *testing.T) {
	builder := NewBuilder()
	doc := be.NewDocument(1).AddConjunction(
		be.NewConjunction().In("A", be.NewIntValues(1)).In("B", be.NewIntValues(1)),
		be.NewConjunction().In("A", be.NewIntValues(2)).In("B", be.NewIntValues(2)),
	)
	if err := builder.AddDocument(doc); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	matched, err := index.Retrieve(planner.Query{Terms: []planner.TermQuery{
		{Field: "A", Value: "1", Op: planner.OpAND},
		{Field: "B", Value: "2", Op: planner.OpAND},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 0 {
		t.Fatalf("cross-conjunction false match: %v", matched)
	}
}

func TestBuilderRejectsUnsupportedExpressions(t *testing.T) {
	cases := []struct {
		name string
		doc  *be.Document
	}{
		{"empty", be.NewDocument(1)},
		{"unsupported operator", be.NewDocument(1).AddConjunction(be.NewConjunction().AddBoolExprs(
			be.NewBoolExpr2("age", be.NewBoolValue(be.ValueOpt(99), int64(1), true)),
		))},
		{"bad range", be.NewDocument(1).AddConjunction(be.NewConjunction().AddBoolExprs(
			be.NewBoolExpr2("age", be.NewBoolValue(be.ValueOptBetween, []int64{25, 0}, true)),
		))},
		{"gt needs integer", be.NewDocument(1).AddConjunction(be.NewConjunction().AddBoolExprs(
			be.NewBoolExpr2("age", be.NewBoolValue(be.ValueOptGT, be.NewStrValues("abc"), true)),
		))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := NewBuilder().AddDocument(tc.doc); err == nil || !errors.Is(err, ErrUnsupportedExpression) {
				t.Fatalf("error=%v, want ErrUnsupportedExpression", err)
			}
		})
	}
}

func TestRetrieveAssignmentsWithRanges(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		// age in [19, +inf)
		be.NewDocument(1).AddConjunction(be.NewConjunction().GreaterThan("age", 18)),
		// age in (-inf, 20)
		be.NewDocument(2).AddConjunction(be.NewConjunction().LessThan("age", 20)),
		// age in [0, 25)
		be.NewDocument(3).AddConjunction(be.NewConjunction().Between("age", 0, 25)),
		// sex in (man) and age in [19, +inf)
		be.NewDocument(4).AddConjunction(be.NewConjunction().In("sex", be.NewStrValues("man")).GreaterThan("age", 18)),
		// age not in [0, 25)
		be.NewDocument(5).AddConjunction(be.NewConjunction().AddBoolExprs(
			be.NewBoolExpr2("age", be.NewBoolValue(be.ValueOptBetween, []int64{0, 25}, false)),
		)),
	}
	if err := builder.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		q    be.Assignments
		want []be.DocID
	}{
		{"gt hit / lt miss / between hit", be.Assignments{"age": be.NewIntValues(20)}, []be.DocID{1, 3, 4}},
		{"gt boundary is strict", be.Assignments{"age": be.NewIntValues(18)}, []be.DocID{2, 3}},
		{"lt boundary is strict", be.Assignments{"age": be.NewIntValues(19)}, []be.DocID{1, 2, 3, 4}},
		{"between right bound excluded", be.Assignments{"age": be.NewIntValues(25)}, []be.DocID{1, 4, 5}},
		{"exclude range hit", be.Assignments{"age": be.NewIntValues(24)}, []be.DocID{1, 3, 4}},
		// absent query fields skip their constraints: sex is not queried, so
		// every range-only group matches; doc4 still needs its sex include hit
		{"unmatched include term", be.Assignments{"sex": be.NewStrValues("other")}, []be.DocID{1, 2, 3, 5}},
		{"include term plus range", be.Assignments{"sex": be.NewStrValues("man"), "age": be.NewIntValues(20)}, []be.DocID{1, 3, 4}},
		{"range miss beats include hit", be.Assignments{"sex": be.NewStrValues("man"), "age": be.NewIntValues(15)}, []be.DocID{2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := index.RetrieveAssignments(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIndexExplain(t *testing.T) {
	builder := NewBuilder()
	if err := builder.AddDocument(be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")))); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	explain, err := index.Explain(planner.Query{Terms: []planner.TermQuery{{Field: "city", Value: "missing", Op: planner.OpAND}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(explain) != 1 || !explain[0].SkippedSegment {
		t.Fatalf("unexpected explain result: %+v", explain)
	}
}
