package parser

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/util"
)

// EncodedPosting is the build-side physical representation of one predicate.
// Record is a container-defined value; the framework routes it to the
// container's AddPosting or AddKeyedPosting method.
type EncodedPosting struct {
	Record any
}

// EncodedQuery is the query-side physical representation of one assignment.
// Value is container-defined and passed directly to IndexReader.MatchQuery.
type EncodedQuery struct {
	Value any
}

// PredicateEncoder is the single translation boundary between business values
// and physical index keys. Builders must call Build, query engines must call
// Query, and segment code must only consume the already-encoded keys. Encoders
// are compiled once with the schema and then reused; implementations must be
// safe for concurrent Query calls after construction.
type PredicateEncoder interface {
	Build(expr *core.ValueExpr) ([]EncodedPosting, error)
	Query(value interface{}) ([]EncodedQuery, error)
}

// FieldCodec is the compiled runtime representation for one schema field.
// It binds the schema map key to its canonical option and reusable encoder.
type FieldCodec struct {
	Field   core.BEField
	Option  core.FieldOption
	Encoder PredicateEncoder
}

// SchemaCodec is a compiled schema. Build and query paths should construct it
// once per schema/load and then reuse the per-field encoders instead of creating
// encoders inside hot document/query loops.
type SchemaCodec struct {
	fields     map[core.BEField]FieldCodec
	schemaHash string
}

// NewSchemaCodec validates a schema and compiles one encoder per field.
func NewSchemaCodec(schema core.Schema) (*SchemaCodec, error) {
	codec := &SchemaCodec{fields: make(map[core.BEField]FieldCodec, len(schema))}
	fields, err := core.NormalizeSchema(schema)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		encoder, err := NewPredicateEncoder(field.Field, field.Option)
		if err != nil {
			return nil, fmt.Errorf("field %s encoder: %w", field.Field, err)
		}
		codec.fields[field.Field] = FieldCodec{Field: field.Field, Option: field.Option, Encoder: encoder}
	}
	codec.schemaHash, err = core.ComputeSchemaHash(schema)
	if err != nil {
		return nil, err
	}
	return codec, nil
}

// SchemaHash returns the canonical fingerprint of this compiled schema.
func (c *SchemaCodec) SchemaHash() string {
	if c == nil {
		return ""
	}
	return c.schemaHash
}

// Field returns the compiled codec for one field.
func (c *SchemaCodec) Field(field core.BEField) (FieldCodec, bool) {
	if c == nil {
		return FieldCodec{}, false
	}
	codec, ok := c.fields[field]
	return codec, ok
}

// Fields returns every compiled field codec ordered by field name. The stable
// ordering makes it the single source of truth for schema iteration (e.g. the
// builder uses it to register fields), keeping built segments deterministic.
func (c *SchemaCodec) Fields() []FieldCodec {
	if c == nil {
		return nil
	}
	out := make([]FieldCodec, 0, len(c.fields))
	for _, fc := range c.fields {
		out = append(out, fc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// EncoderFactory constructs a PredicateEncoder from a field and its option.
type EncoderFactory func(field core.BEField, option core.FieldOption) (PredicateEncoder, error)

var predicateEncoderFactories = map[string]EncoderFactory{}

func init() {
	RegisterPredicateEncoder(core.IndexNameDefault, func(core.BEField, core.FieldOption) (PredicateEncoder, error) {
		return newExactTermEncoder("default")
	})
	RegisterPredicateEncoder("number", func(core.BEField, core.FieldOption) (PredicateEncoder, error) {
		return newExactTermEncoder("number")
	})
	RegisterPredicateEncoder(core.IndexNameACMatcher, func(core.BEField, core.FieldOption) (PredicateEncoder, error) { return ACEncoder{}, nil })
	RegisterPredicateEncoder(core.IndexNameExtendRange, func(core.BEField, core.FieldOption) (PredicateEncoder, error) { return RangeEncoder{}, nil })
}

// RegisterPredicateEncoder installs or replaces the encoder factory for an
// index type. New physical index types should register here instead of
// adding builder/engine switch branches.
func RegisterPredicateEncoder(container string, factory EncoderFactory) {
	predicateEncoderFactories[container] = factory
}

// NewPredicateEncoder creates the encoder specified by FieldOption.Encoder.
// If empty, it uses the default exact-term encoder. Encoder and IndexType are independent
// choices — Encoder describes how values become physical keys, IndexType
// describes how those keys are stored and queried.
func NewPredicateEncoder(field core.BEField, option core.FieldOption) (PredicateEncoder, error) {
	normalized, err := core.NormalizeFieldOption(field, option)
	if err != nil {
		return nil, err
	}
	encoder := normalized.Encoder
	factory, ok := predicateEncoderFactories[encoder]
	if !ok || factory == nil {
		return nil, fmt.Errorf("%w: %s", core.ErrUnknownContainer, encoder)
	}
	return factory(field, normalized)
}

func newExactTermEncoder(tokenizerName string) (PredicateEncoder, error) {
	tokenizer, ok := NewValueTokenizer(tokenizerName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", core.ErrFieldIndexMissing, tokenizerName)
	}
	return ExactTermEncoder{tokenizer: tokenizer}, nil
}

// ExactTermEncoder adapts ValueTokenizer to the PredicateEncoder contract for
// dictionary/posting-list backed fields.
type ExactTermEncoder struct {
	tokenizer ValueTokenizer
}

func (e ExactTermEncoder) Build(expr *core.ValueExpr) ([]EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("nil value expression")
	}
	if expr.Operator != core.ValueOptEQ {
		return nil, fmt.Errorf("%w: exact term encoder only supports EQ, got %d", core.ErrUnsupportedPredicate, expr.Operator)
	}
	terms, err := e.tokenizer.TokenizeValue(expr.Value)
	if err != nil {
		return nil, err
	}
	terms = util.DistinctString(terms)
	out := make([]EncodedPosting, 0, len(terms))
	for _, term := range terms {
		out = append(out, EncodedPosting{Record: term})
	}
	return out, nil
}

func (e ExactTermEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	terms, err := e.tokenizer.TokenizeAssign(value)
	if err != nil {
		return nil, err
	}
	terms = util.DistinctString(terms)
	out := make([]EncodedQuery, 0, len(terms))
	for _, term := range terms {
		out = append(out, EncodedQuery{Value: term})
	}
	return out, nil
}

// RangeEncoder translates range predicates into closed integer intervals and
// query assignments into a single stabbing point for the ext_range container.
type RangeEncoder struct{}

func (RangeEncoder) Build(expr *core.ValueExpr) ([]EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("nil value expression")
	}
	intervals, err := ParseRangeExpr(expr.Operator, expr.Value)
	if err != nil {
		return nil, err
	}
	out := make([]EncodedPosting, 0, len(intervals))
	for _, iv := range intervals {
		out = append(out, EncodedPosting{Record: core.RangeRecord{Lo: iv.Lo, Hi: iv.Hi}})
	}
	return out, nil
}

func (RangeEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	point, err := ParseRangePoint(value)
	if err != nil {
		return nil, err
	}
	return []EncodedQuery{{Value: point}}, nil
}

// ACEncoder translates build-side patterns and query-side text for the AC
// matcher container. AC patterns are still stored as term postings so the
// segment writer can build the automaton from the same term dictionary.
type ACEncoder struct{}

func (ACEncoder) Build(expr *core.ValueExpr) ([]EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("nil value expression")
	}
	if expr.Operator != core.ValueOptEQ {
		return nil, fmt.Errorf("%w: ac encoder only supports EQ, got %d", core.ErrUnsupportedPredicate, expr.Operator)
	}
	patterns, err := ValuesToStrings(expr.Value)
	if err != nil {
		return nil, err
	}
	patterns = util.DistinctString(patterns)
	out := make([]EncodedPosting, 0, len(patterns))
	for _, pattern := range patterns {
		out = append(out, EncodedPosting{Record: pattern})
	}
	return out, nil
}

func (ACEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	parts, err := ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []EncodedQuery{{Value: strings.Join(parts, " ")}}, nil
}
