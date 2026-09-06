package core

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
)

const schemaHashDomain = "be_indexer/schema/v1"

// NormalizeFieldOption returns the canonical indexing configuration for a field.
func NormalizeFieldOption(field BEField, option FieldOption) (FieldOption, error) {
	if field == "" {
		return FieldOption{}, fmt.Errorf("field name is required")
	}
	normalized := option
	if normalized.IndexType == "" {
		normalized.IndexType = IndexNameDefault
	}
	// Preserve the current runtime semantics: an omitted encoder means the
	// default exact-term encoder, independently of IndexType.
	if normalized.Encoder == "" {
		normalized.Encoder = IndexNameDefault
	}
	return normalized, nil
}

type SchemaField struct {
	Field  BEField
	Option FieldOption
}

// NormalizeSchema validates and returns fields in deterministic name order.
func NormalizeSchema(schema Schema) ([]SchemaField, error) {
	if len(schema) == 0 {
		return nil, fmt.Errorf("fields are required")
	}
	normalized := make([]SchemaField, 0, len(schema))
	for field, option := range schema {
		canonical, err := NormalizeFieldOption(field, option)
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, SchemaField{Field: field, Option: canonical})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Field < normalized[j].Field
	})
	return normalized, nil
}

// ComputeSchemaHash returns a deterministic fingerprint for canonical field
// semantics. Map order and explicit-vs-default option spelling do not affect
// the result; changing a field name, index type, or encoder does.
func ComputeSchemaHash(schema Schema) (string, error) {
	normalized, err := NormalizeSchema(schema)
	if err != nil {
		return "", err
	}
	return computeNormalizedSchemaHash(normalized), nil
}

func computeNormalizedSchemaHash(normalized []SchemaField) string {
	h := sha256.New()
	writeSchemaHashString(h, schemaHashDomain)
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], uint32(len(normalized)))
	_, _ = h.Write(count[:])
	for _, field := range normalized {
		writeSchemaHashString(h, field.Field)
		writeSchemaHashString(h, field.Option.IndexType)
		writeSchemaHashString(h, field.Option.Encoder)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func writeSchemaHashString(h hash.Hash, value string) {
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(value))
}
