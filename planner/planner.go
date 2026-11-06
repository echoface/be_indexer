package planner

import (
	"fmt"
	"sort"

	"github.com/echoface/be_indexer/storage"
)

// Operator describes how a term condition participates in a query.
type Operator uint8

const (
	OpAND Operator = iota
	OpOR
	OpNOT
)

func (op Operator) String() string {
	switch op {
	case OpAND:
		return "AND"
	case OpOR:
		return "OR"
	case OpNOT:
		return "NOT"
	default:
		return "UNKNOWN"
	}
}

// TermQuery is one exact field/value condition.
type TermQuery struct {
	Field string
	Value string
	Op    Operator
}

// Query is a conjunction of positive/negative terms. OR groups are represented
// by terms with OpOR and are evaluated as one union before the outer AND.
type Query struct {
	Terms []TermQuery
}

type Strategy uint8

const (
	StrategyCursor Strategy = iota
	StrategyBitmap
)

func (s Strategy) String() string {
	if s == StrategyBitmap {
		return "bitmap"
	}
	return "cursor"
}

// TermPlan records statistics and the selected execution strategy.
type TermPlan struct {
	Query       TermQuery
	TermID      storage.TermID
	Cardinality uint32
	Bytes       uint32
	Encoding    string
	Strategy    Strategy
	Skip        bool
}

// Plan is an immutable query execution plan.
type Plan struct {
	Terms          []TermPlan
	EstimatedHits  uint64
	EstimatedCost  uint64
	SkippedSegment bool
}

// Planner creates plans from immutable Segment statistics.
type Planner struct {
	// BitmapThreshold is the minimum posting cardinality at which bitmap
	// execution is preferred. It is deliberately configurable for benchmarks.
	BitmapThreshold uint32
}

func New() *Planner {
	return &Planner{BitmapThreshold: 1024}
}

// Plan builds a deterministic plan and skips the Segment when a required term
// does not exist. Positive AND terms are ordered by cardinality; NOT terms are
// delayed until after the positive candidate set is formed.
func (p *Planner) Plan(segment *storage.Segment, query Query) (Plan, error) {
	if segment == nil {
		return Plan{}, fmt.Errorf("segment is nil")
	}
	result := Plan{Terms: make([]TermPlan, 0, len(query.Terms))}
	for _, term := range query.Terms {
		id, ok := segment.LookupTerm(term.Field, term.Value)
		if !ok {
			if term.Op == OpNOT {
				continue
			}
			result.SkippedSegment = true
			return result, nil
		}
		info, err := segment.InspectTerm(term.Field, term.Value)
		if err != nil {
			return Plan{}, err
		}
		strategy := StrategyCursor
		if info.Count >= p.BitmapThreshold || info.Encoding == storage.PostingRaw {
			strategy = StrategyBitmap
		}
		result.Terms = append(result.Terms, TermPlan{
			Query: term, TermID: id, Cardinality: info.Count,
			Bytes: info.Bytes, Encoding: info.Encoding.String(), Strategy: strategy,
		})
	}
	result.sortTerms()
	result.EstimatedHits = estimateHits(result.Terms)
	result.EstimatedCost = estimateCost(result.Terms)
	return result, nil
}

func (p Plan) sortTerms() {
	sort.SliceStable(p.Terms, func(i, j int) bool {
		a, b := p.Terms[i], p.Terms[j]
		if a.Query.Op == OpNOT && b.Query.Op != OpNOT {
			return false
		}
		if a.Query.Op != OpNOT && b.Query.Op == OpNOT {
			return true
		}
		if a.Cardinality == b.Cardinality {
			if a.Query.Field == b.Query.Field {
				return a.Query.Value < b.Query.Value
			}
			return a.Query.Field < b.Query.Field
		}
		return a.Cardinality < b.Cardinality
	})
}

func estimateHits(terms []TermPlan) uint64 {
	var estimate uint64
	for _, term := range terms {
		if term.Query.Op == OpNOT {
			continue
		}
		if estimate == 0 || uint64(term.Cardinality) < estimate {
			estimate = uint64(term.Cardinality)
		}
	}
	return estimate
}

func estimateCost(terms []TermPlan) uint64 {
	var cost uint64
	for _, term := range terms {
		cost += uint64(term.Cardinality)
	}
	return cost
}
