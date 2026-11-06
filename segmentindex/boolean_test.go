package segmentindex

import (
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
)

func TestRetrieveAssignmentsIncludeExcludeAndWildcard(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("bj")).In("kind", be.NewStrValues("ad"))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().NotIn("city", be.NewStrValues("sh"))),
		be.NewDocument(3).AddConjunction(be.NewConjunction()),
	}
	if err := builder.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.RetrieveAssignments(be.Assignments{"city": be.NewStrValues("bj")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{1, 2, 3}) {
		t.Fatalf("got %v", got)
	}
	got, err = index.RetrieveAssignments(be.Assignments{"city": be.NewStrValues("sh")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []be.DocID{3}) {
		t.Fatalf("exclude got %v", got)
	}
}
