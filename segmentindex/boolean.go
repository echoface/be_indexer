package segmentindex

import (
	"sort"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

// RetrieveAssignments evaluates the exact Include/Exclude DNF semantics using
// field-local unions and conjunction-local intersections. Numeric range
// predicates (GT/LT/Between) recorded at build time are evaluated here as
// well; use this entry point (not Index.Retrieve) when documents contain
// ranges.
func (i *Index) RetrieveAssignments(assignments be.Assignments) ([]be.DocID, error) {
	return i.retrieveAssignmentsFiltered(assignments, nil)
}

// retrieveAssignmentsFiltered behaves like RetrieveAssignments but drops
// result ids whose group versions are marked dead by tomb.
func (i *Index) retrieveAssignmentsFiltered(assignments be.Assignments, tomb *tombstoneSet) ([]be.DocID, error) {
	if i == nil {
		return nil, ErrUnsupportedExpression
	}
	seen := make(map[be.DocID]struct{})
	for idx, segment := range i.segments {
		// Each Segment represents one conjunction. Query evaluation is performed
		// against the terms encoded for that conjunction.
		ids, err := evaluateGroup(segment, i.groups[idx], assignments)
		if err != nil {
			return nil, err
		}
		meta := i.groups[idx]
		for _, id := range ids {
			if meta.dead(id, tomb) {
				continue
			}
			seen[be.DocID(id)] = struct{}{}
		}
	}
	result := make([]be.DocID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(a, b int) bool { return result[a] < result[b] })
	return result, nil
}

// groupFields merges the conjunction's fields with the segment's posting
// fields: range-only fields carry no postings and would be missed otherwise.
func groupFields(meta groupMeta, segmentFields []string) []string {
	set := make(map[string]struct{}, len(meta.shapes)+len(segmentFields))
	for field := range meta.shapes {
		set[field] = struct{}{}
	}
	for _, field := range segmentFields {
		set[field] = struct{}{}
	}
	fields := make([]string, 0, len(set))
	for field := range set {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func evaluateGroup(segment interface {
	Fields() []string
	PostingFor(string, string) ([]uint64, error)
	AllDocIDs() ([]uint64, error)
	HasTermPrefix(string, string) bool
}, meta groupMeta, assignments be.Assignments) ([]uint64, error) {
	var result []uint64
	for _, field := range groupFields(meta, segment.Fields()) {
		values, present := assignments[be.BEField(field)]
		if !present {
			continue
		}
		queryValues, err := parser.ValuesToStrings(values)
		if err != nil {
			return nil, err
		}
		includes := make([][]uint64, 0)
		excludes := make([][]uint64, 0)
		for _, value := range queryValues {
			if posting, err := segment.PostingFor(field, includeValuePrefix+value); err == nil {
				includes = append(includes, posting)
			}
			if posting, err := segment.PostingFor(field, excludeValuePrefix+value); err == nil {
				excludes = append(excludes, posting)
			}
		}
		fieldResult, err := unionPostings(includes)
		if err != nil {
			return nil, err
		}
		// an include range satisfied by the query values passes every live doc,
		// mirroring a hit on an include posting
		inclHit, exclHit, hasInclRange := evaluateFieldRanges(meta.ranges, field, values)
		if inclHit {
			live, err := segment.AllDocIDs()
			if err != nil {
				return nil, err
			}
			fieldResult, err = unionPostings([][]uint64{fieldResult, live})
			if err != nil {
				return nil, err
			}
		}
		// a field without include terms or include ranges accepts every live
		// doc; the shape (not the merged postings) decides this so coalesced
		// groups keep the fallback semantics of their source conjunctions
		if len(fieldResult) == 0 && !meta.shapes[field].hasIncl && !hasInclRange {
			fieldResult, err = segment.AllDocIDs()
			if err != nil {
				return nil, err
			}
		}
		for _, posting := range excludes {
			fieldResult = difference(fieldResult, posting)
		}
		// an exclude range hit by the query values removes every live doc
		if exclHit {
			live, err := segment.AllDocIDs()
			if err != nil {
				return nil, err
			}
			fieldResult = difference(fieldResult, live)
		}
		if result == nil {
			result = fieldResult
		} else {
			result = intersect(result, fieldResult)
		}
	}
	if result == nil {
		return segment.AllDocIDs()
	}
	return result, nil
}

func unionPostings(groups [][]uint64) ([]uint64, error) {
	seen := make(map[uint64]struct{})
	for _, group := range groups {
		for _, id := range group {
			seen[id] = struct{}{}
		}
	}
	result := make([]uint64, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
func intersect(a, b []uint64) []uint64 {
	result := make([]uint64, 0)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] < b[j] {
			i++
		} else if a[i] > b[j] {
			j++
		} else {
			result = append(result, a[i])
			i++
			j++
		}
	}
	return result
}
func difference(a, b []uint64) []uint64 {
	result := make([]uint64, 0, len(a))
	j := 0
	for _, id := range a {
		for j < len(b) && b[j] < id {
			j++
		}
		if j >= len(b) || b[j] != id {
			result = append(result, id)
		}
	}
	return result
}
