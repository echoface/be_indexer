package be_indexer

import "github.com/echoface/be_indexer/core"

type (
	DocID       = core.DocID
	DocIDList   = core.DocIDList
	FieldName   = core.FieldName
	Values      = core.Values
	ValueOp     = core.ValueOp
	Assignments = core.Assignments
	Document    = core.Document
	Conjunction = core.Conjunction
	Predicate   = core.Predicate
	Constraint  = core.Constraint
	FieldOption = core.FieldOption
	Schema      = core.Schema
)

const (
	MaxDocID             = core.MaxDocID
	ValueOpEQ            = core.ValueOpEQ
	ValueOpGT            = core.ValueOpGT
	ValueOpLT            = core.ValueOpLT
	ValueOpBetween       = core.ValueOpBetween
	IndexNameDefault     = core.IndexNameDefault
	IndexNameACMatcher   = core.IndexNameACMatcher
	IndexNameExtendRange = core.IndexNameExtendRange
)

// NewDocument creates an empty DNF document.
func NewDocument(id DocID) *Document { return core.NewDocument(id) }

// NewConjunction creates an empty AND clause.
func NewConjunction() *Conjunction { return core.NewConjunction() }

// NewPredicate creates an equality include/exclude predicate.
func NewPredicate(field FieldName, include bool, values Values) *Predicate {
	return core.NewPredicate(field, include, values)
}

// NewPredicateWithConstraint associates a field with an explicit constraint.
func NewPredicateWithConstraint(field FieldName, expr Constraint) *Predicate {
	return core.NewPredicateWithConstraint(field, expr)
}

// NewConstraint creates a constraint with an explicit operator and polarity.
func NewConstraint(op ValueOp, values Values, include bool) Constraint {
	return core.NewConstraint(op, values, include)
}

// NewIntValues constructs a non-empty integer value list.
func NewIntValues(first int, rest ...int) Values { return core.NewIntValues(first, rest...) }

// NewInt32Values constructs a non-empty int32 value list.
func NewInt32Values(first int32, rest ...int32) Values { return core.NewInt32Values(first, rest...) }

// NewInt64Values constructs a non-empty int64 value list.
func NewInt64Values(first int64, rest ...int64) Values { return core.NewInt64Values(first, rest...) }

// NewStrValues constructs a non-empty string value list.
func NewStrValues(first string, rest ...string) Values { return core.NewStrValues(first, rest...) }
