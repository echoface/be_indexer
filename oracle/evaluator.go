package oracle

import (
	"fmt"
	"sort"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
)

// EvaluateDocument evaluates a DNF document with the same current production
// semantics as the indexer for EQ predicates:
//   - include requires the query field to be present and intersect predicate values;
//   - exclude only fires when the query field is present and intersects predicate values;
//   - query-missing does not trigger exclude.
func EvaluateDocument(doc *core.Document, assigns core.Assignments) (bool, error) {
	if doc == nil {
		return false, nil
	}
	for _, conj := range doc.Cons {
		matched, err := EvaluateConjunction(conj, assigns)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// EvaluateConjunction evaluates a conjunction under exclude-on-present-value semantics.
func EvaluateConjunction(conj *core.Conjunction, assigns core.Assignments) (bool, error) {
	if conj == nil {
		return false, nil
	}
	for field, exprs := range conj.Predicates {
		queryValues := normalize(assigns[field])
		queryPresent := len(queryValues) > 0
		for _, expr := range exprs {
			if expr == nil {
				return false, fmt.Errorf("nil predicate expression for field %s", field)
			}

			var intersects bool
			if expr.Operator == core.ValueOptEQ {
				predicateValues := normalize(expr.Value)
				intersects = queryPresent && hasIntersection(queryValues, predicateValues)
			} else {
				// Range predicate (>, <, between): the query field must be a
				// single integer point that falls inside one of the intervals
				// the predicate expands to.
				hit, err := rangeHit(expr, assigns[field], queryPresent)
				if err != nil {
					return false, err
				}
				intersects = hit
			}

			if expr.Incl {
				if !intersects {
					return false, nil
				}
				continue
			}
			if intersects {
				return false, nil
			}
		}
	}
	return true, nil
}

// rangeHit reports whether the query value for a range predicate lies inside any
// interval the predicate expands to. Mirrors exclude-on-present-value: a missing
// query field never intersects.
func rangeHit(expr *core.ValueExpr, queryValue interface{}, queryPresent bool) (bool, error) {
	if !queryPresent {
		return false, nil
	}
	intervals, err := parser.ParseRangeExpr(expr.Operator, expr.Value)
	if err != nil {
		return false, err
	}
	point, err := parser.ParseRangePoint(queryValue)
	if err != nil {
		// A non-integer or multi-valued query for a range field cannot stab.
		return false, nil
	}
	for _, iv := range intervals {
		if iv.Lo <= point && point <= iv.Hi {
			return true, nil
		}
	}
	return false, nil
}

// MatchDocuments evaluates docs and returns sorted matching DocIDs.
func MatchDocuments(docs []*core.Document, assigns core.Assignments) (core.DocIDList, error) {
	ids := make(core.DocIDList, 0)
	seen := map[core.DocID]struct{}{}
	for _, doc := range docs {
		matched, err := EvaluateDocument(doc, assigns)
		if err != nil {
			return nil, err
		}
		if matched {
			if _, ok := seen[doc.ID]; !ok {
				seen[doc.ID] = struct{}{}
				ids = append(ids, doc.ID)
			}
		}
	}
	sort.Sort(ids)
	return ids, nil
}

func normalize(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return []string{t}
	case int:
		return []string{fmt.Sprintf("%d", t)}
	case int32:
		return []string{fmt.Sprintf("%d", t)}
	case int64:
		return []string{fmt.Sprintf("%d", t)}
	case uint:
		return []string{fmt.Sprintf("%d", t)}
	case uint32:
		return []string{fmt.Sprintf("%d", t)}
	case uint64:
		return []string{fmt.Sprintf("%d", t)}
	case []string:
		return append([]string(nil), t...)
	case []int:
		out := make([]string, 0, len(t))
		for _, v := range t {
			out = append(out, fmt.Sprintf("%d", v))
		}
		return out
	case []int32:
		out := make([]string, 0, len(t))
		for _, v := range t {
			out = append(out, fmt.Sprintf("%d", v))
		}
		return out
	case []int64:
		out := make([]string, 0, len(t))
		for _, v := range t {
			out = append(out, fmt.Sprintf("%d", v))
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, v := range t {
			out = append(out, normalize(v)...)
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", t)}
	}
}

func hasIntersection(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}
