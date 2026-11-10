package segmentindex

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
)

func TestIndexMarshalLoadRoundTrip(t *testing.T) {
	builder := NewBuilder()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(
			be.NewConjunction().
				In("city", be.NewStrValues("bj", "gz")).
				In("device", be.NewStrValues("ios")).
				Between("age", 25, 26),
		),
		be.NewDocument(2).AddConjunction(
			be.NewConjunction().
				In("city", be.NewStrValues("sh")).
				NotIn("device", be.NewStrValues("android", "tablet")),
		),
		be.NewDocument(3).AddConjunction(
			be.NewConjunction().In("city", be.NewStrValues("bj")).GreaterThan("age", 20),
			be.NewConjunction().In("city", be.NewStrValues("cd")).LessThan("age", 20),
		),
	}
	if err := builder.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	original, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := original.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex(encoded)
	if err != nil {
		t.Fatal(err)
	}

	reencoded, err := loaded.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("marshal after load is not deterministic")
	}

	query := be.Assignments{
		"city":   be.NewStrValues("bj", "gz"),
		"device": be.NewStrValues("ios"),
		"age":    be.NewIntValues(25),
	}
	want, err := original.RetrieveAssignments(query)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loaded.RetrieveAssignments(query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query result got %v want %v", got, want)
	}

	rangeQuery := be.Assignments{"age": be.NewIntValues(21)}
	want, err = original.RetrieveAssignments(rangeQuery)
	if err != nil {
		t.Fatal(err)
	}
	got, err = loaded.RetrieveAssignments(rangeQuery)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("range query result got %v want %v", got, want)
	}
}

func TestIndexMarshalLoadEmpty(t *testing.T) {
	original, err := NewBuilder().Seal()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := original.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadIndex(encoded)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loaded.RetrieveAssignments(be.Assignments{"city": be.NewStrValues("bj")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Fatalf("empty index returned %v", result)
	}
}

func TestLoadIndexRejectsCorruptAndTruncatedData(t *testing.T) {
	builder := NewBuilder()
	if err := builder.AddDocument(be.NewDocument(1).AddConjunction(
		be.NewConjunction().In("city", be.NewStrValues("bj")),
	)); err != nil {
		t.Fatal(err)
	}
	index, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := index.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-5] ^= 0xff
	if _, err := LoadIndex(corrupt); !errors.Is(err, ErrInvalidIndex) {
		t.Fatalf("corrupt data error=%v, want ErrInvalidIndex", err)
	}

	for _, length := range []int{0, 1, indexHeaderSize, len(encoded) - 1} {
		if _, err := LoadIndex(encoded[:length]); !errors.Is(err, ErrInvalidIndex) {
			t.Fatalf("truncated length=%d error=%v, want ErrInvalidIndex", length, err)
		}
	}
}
