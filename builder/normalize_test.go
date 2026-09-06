package builder

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
)

func normFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault, Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault}},
	}
}

func newTestCodec(t *testing.T) *parser.SchemaCodec {
	t.Helper()
	codec, err := parser.NewSchemaCodec(normFields())
	if err != nil {
		t.Fatalf("NewSchemaCodec: %v", err)
	}
	return codec
}

// --- §4.1.2: unindexed fields ---------------------------------------------------

func TestNormalize_UnindexedFieldRejectedByDefault(t *testing.T) {
	codec := newTestCodec(t)
	doc := core.NewDocument(1).AddConjunction(
		core.NewConjunction().In("age", 18).In("unknown", "x"),
	)
	_, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{})
	if err == nil {
		t.Fatal("expected error for unindexed field under default options")
	}
	if !strings.Contains(err.Error(), "unindexed field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalize_UnindexedIncludeDroppedFromK(t *testing.T) {
	codec := newTestCodec(t)
	// age (indexed include) + unknown (unindexed include). With IgnoreUnindexed,
	// K must count only the indexed include => K=1, not 2. If unknown inflated K
	// to 2 the conjunction could never be satisfied (false negative).
	doc := core.NewDocument(1).AddConjunction(
		core.NewConjunction().In("age", 18).In("unknown", "x"),
	)
	norm, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{IgnoreUnindexedFields: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := norm.Cons[0].CalcConjSize(); got != 1 {
		t.Fatalf("normalized K = %d, want 1 (unindexed include must be dropped)", got)
	}
	if _, ok := norm.Cons[0].Predicates["unknown"]; ok {
		t.Fatal("unindexed field must be dropped from normalized conjunction")
	}
	// Original doc must not be mutated.
	if _, ok := doc.Cons[0].Predicates["unknown"]; !ok {
		t.Fatal("input document was mutated; normalization must copy")
	}
}

func TestNormalize_UnindexedIncludeFalseNegativeGuard(t *testing.T) {
	// End-to-end: with default (reject) options a doc with an unindexed include
	// fails the build rather than silently producing a doc that can never match.
	fields := normFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 18).In("ghost", "x")),
	}
	buf := new(bytes.Buffer)
	_, err := BuildSegmentFromDocs(buf, fields, docs)
	if err == nil {
		t.Fatal("expected build failure for unindexed include field")
	}
}

// --- §4.1.6: multiple include constraints on one field --------------------------

func TestNormalize_MultipleIncludeRejected(t *testing.T) {
	codec := newTestCodec(t)
	// age > 18 AND age < 60 modeled as two includes on the same field: ambiguous.
	conj := &core.Conjunction{Predicates: map[core.BEField][]*core.ValueExpr{
		"age": {
			{Incl: true, Operator: core.ValueOptGT, Value: int64(18)},
			{Incl: true, Operator: core.ValueOptLT, Value: int64(60)},
		},
	}}
	doc := &core.Document{ID: 1, Cons: []*core.Conjunction{conj}}
	_, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{})
	if err == nil {
		t.Fatal("expected error for multiple include constraints on one field")
	}
	if !strings.Contains(err.Error(), "include constraint") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalize_MultipleExcludeAllowed(t *testing.T) {
	codec := newTestCodec(t)
	conj := &core.Conjunction{Predicates: map[core.BEField][]*core.ValueExpr{
		"city": {
			{Incl: false, Operator: core.ValueOptEQ, Value: "bj"},
			{Incl: false, Operator: core.ValueOptEQ, Value: "sh"},
		},
	}}
	doc := &core.Document{ID: 1, Cons: []*core.Conjunction{conj}}
	if _, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{}); err != nil {
		t.Fatalf("multiple exclude constraints must be allowed, got: %v", err)
	}
}

func TestNormalize_SingleIncludeMultiValueAllowed(t *testing.T) {
	codec := newTestCodec(t)
	// One include constraint carrying multiple values (OR within the constraint)
	// is the sanctioned way to express set membership and must be accepted.
	doc := core.NewDocument(1).AddConjunction(
		core.NewConjunction().In("city", core.NewStrValues("bj", "sh")),
	)
	if _, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{}); err != nil {
		t.Fatalf("single multi-value include must be allowed, got: %v", err)
	}
}

// --- §4.1.4 / basic validation --------------------------------------------------

func TestNormalize_NilConjunctionAndConstraint(t *testing.T) {
	codec := newTestCodec(t)

	nilConj := &core.Document{ID: 1, Cons: []*core.Conjunction{nil}}
	if _, err := ValidateAndNormalizeDocument(codec, nilConj, NormalizeOptions{}); err == nil {
		t.Fatal("expected error for nil conjunction")
	}

	nilConstraint := &core.Document{ID: 1, Cons: []*core.Conjunction{
		{Predicates: map[core.BEField][]*core.ValueExpr{"age": {nil}}},
	}}
	if _, err := ValidateAndNormalizeDocument(codec, nilConstraint, NormalizeOptions{}); err == nil {
		t.Fatal("expected error for nil constraint")
	}
}

func TestNormalize_InvalidDocID(t *testing.T) {
	codec := newTestCodec(t)
	doc := core.NewDocument(1 << 60).AddConjunction(core.NewConjunction().In("age", 18))
	if _, err := ValidateAndNormalizeDocument(codec, doc, NormalizeOptions{}); err == nil {
		t.Fatal("expected error for out-of-range DocID")
	}
}

// --- §4.1.5: duplicate DocID in batch full build --------------------------------

func TestBatchBuild_DuplicateDocIDRejected(t *testing.T) {
	fields := normFields()
	docs := []*core.Document{
		core.NewDocument(7).AddConjunction(core.NewConjunction().In("age", 18)),
		core.NewDocument(7).AddConjunction(core.NewConjunction().In("age", 20)),
	}
	buf := new(bytes.Buffer)
	_, err := BuildSegmentFromDocs(buf, fields, docs)
	if err == nil {
		t.Fatal("expected duplicate doc id to be rejected in batch full build")
	}
	if !strings.Contains(err.Error(), "duplicate doc id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- §4.1.1: IndexType validation -----------------------------------------------

func TestBuild_UnknownIndexTypeRejected(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"age": {ID: 1, Field: "age", FieldOption: core.FieldOption{IndexType: "no_such_container"}},
	}
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 18)),
	}
	buf := new(bytes.Buffer)
	_, err := BuildSegmentFromDocs(buf, fields, docs)
	if err == nil {
		t.Fatal("expected unknown IndexType to be rejected at build time")
	}
	if !errors.Is(err, core.ErrUnknownContainer) {
		t.Fatalf("expected ErrUnknownContainer, got: %v", err)
	}
}
