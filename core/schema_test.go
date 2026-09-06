package core

import "testing"

func TestComputeSchemaHashIsCanonical(t *testing.T) {
	left, err := ComputeSchemaHash(Schema{
		"city": {},
		"age":  {Encoder: "number"},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := ComputeSchemaHash(Schema{
		"age":  {IndexType: IndexNameDefault, Encoder: "number"},
		"city": {IndexType: IndexNameDefault, Encoder: IndexNameDefault},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("equivalent schemas differ: %s != %s", left, right)
	}
}

func TestComputeSchemaHashDetectsSemanticChanges(t *testing.T) {
	base := Schema{"age": {IndexType: IndexNameDefault, Encoder: "number"}}
	baseHash, err := ComputeSchemaHash(base)
	if err != nil {
		t.Fatal(err)
	}
	changes := []Schema{
		{"years": {IndexType: IndexNameDefault, Encoder: "number"}},
		{"age": {IndexType: IndexNameExtendRange, Encoder: "number"}},
		{"age": {IndexType: IndexNameDefault, Encoder: IndexNameDefault}},
	}
	for _, changed := range changes {
		hash, err := ComputeSchemaHash(changed)
		if err != nil {
			t.Fatal(err)
		}
		if hash == baseHash {
			t.Fatalf("semantic schema change preserved hash %s: %+v", hash, changed)
		}
	}
}

func TestNormalizeSchemaRejectsInvalidDefinitions(t *testing.T) {
	if _, err := NormalizeSchema(nil); err == nil {
		t.Fatal("expected empty schema error")
	}
	if _, err := NormalizeSchema(Schema{"": {}}); err == nil {
		t.Fatal("expected empty field name error")
	}
}
