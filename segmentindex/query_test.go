package segmentindex

import (
	"math"
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
)

func TestRetrieveAssignmentsTypedNumericSemantics(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("n", be.NewInt64Values(1))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("n", be.NewStrValues("01"))),
		be.NewDocument(3).AddConjunction(be.NewConjunction().In("n", be.NewInt64Values(math.MaxInt64))),
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
		{"numeric query matches canonical numeric term", be.Assignments{"n": be.NewInt64Values(1)}, []be.DocID{1}},
		{"string query matches canonical numeric term", be.Assignments{"n": be.NewStrValues("1")}, []be.DocID{1}},
		{"numeric query excludes non-canonical string term", be.Assignments{"n": be.NewInt64Values(1)}, []be.DocID{1}},
		{"numeric max matches numeric max", be.Assignments{"n": be.NewInt64Values(math.MaxInt64)}, []be.DocID{3}},
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

	got, err := index.RetrieveAssignments(be.Assignments{"n": be.NewStrValues("01")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{2}) {
		t.Fatalf("non-canonical string query got %v want [2]", got)
	}
}

func TestRetrieveAssignmentsIntoReusesResultBuffer(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("n", be.NewInt64Values(1))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("n", be.NewInt64Values(2))),
	}
	if err := builder.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]be.DocID, 1, 8)
	got, err := index.RetrieveAssignmentsInto(buf, be.Assignments{"n": be.NewInt64Values(2)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{2}) {
		t.Fatalf("got %v want [2]", got)
	}
	if cap(got) != cap(buf) {
		t.Fatalf("result buffer was not reused: cap=%d want=%d", cap(got), cap(buf))
	}

	got, err = index.RetrieveAssignmentsInto(got[:0], be.Assignments{"n": be.NewInt64Values(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{1}) {
		t.Fatalf("reused buffer got %v want [1]", got)
	}
}
