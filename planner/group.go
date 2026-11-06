package planner

import "github.com/echoface/be_indexer/storage"

// GroupQuery preserves DNF boundaries: terms inside one group are ANDed,
// groups are ORed. This prevents postings from different conjunctions being
// combined into a false match.
type GroupQuery struct {
	Terms []TermQuery
}

// PlanGroups plans every DNF group independently and preserves group order.
func (p *Planner) PlanGroups(segment *storage.Segment, query []GroupQuery) ([]Plan, error) {
	plans := make([]Plan, 0, len(query))
	for _, group := range query {
		plan, err := p.Plan(segment, Query{Terms: group.Terms})
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// ExecuteGroups evaluates OR-of-AND groups and deduplicates document IDs.
func ExecuteGroups(segment *storage.Segment, plans []Plan) ([]uint64, error) {
	seen := make(map[uint64]struct{})
	for _, plan := range plans {
		ids, err := Execute(segment, plan)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			seen[id] = struct{}{}
		}
	}
	result := make([]uint64, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sortUint64(result)
	return result, nil
}

func sortUint64(values []uint64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
