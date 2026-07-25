package mph

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

type Encoder struct{}

var _ parser.PredicateEncoder = Encoder{}

func (Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("mph: nil value expression")
	}
	if expr.Operator != core.ValueOptEQ {
		return nil, fmt.Errorf("%w: mph encoder only supports EQ, got %d", core.ErrUnsupportedPredicate, expr.Operator)
	}
	terms, err := parser.ValuesToStrings(expr.Value)
	if err != nil {
		return nil, err
	}
	terms = util.DistinctString(terms)
	out := make([]parser.EncodedPosting, 0, len(terms))
	for _, term := range terms {
		out = append(out, parser.EncodedPosting{Record: term})
	}
	return out, nil
}

func (Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	terms, err := parser.ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	terms = util.DistinctString(terms)
	out := make([]parser.EncodedQuery, 0, len(terms))
	for _, term := range terms {
		out = append(out, parser.EncodedQuery{
			Kind:  parser.QueryKind(ContainerName),
			Value: term,
		})
	}
	return out, nil
}
