// Package segmentindex adapts Document/Conjunction expressions to immutable
// Segment groups and planner execution.
package segmentindex

import (
	"fmt"
	"sort"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/planner"
	"github.com/echoface/be_indexer/storage"
)

var ErrUnsupportedExpression = fmt.Errorf("expression is not supported by segment index")

const (
	includeValuePrefix = "\x01"
	excludeValuePrefix = "\x02"
	wildcardValue      = "\x03"
)

// rangePredicate is defined in range.go.

// fieldShape records which exact-term expression kinds a conjunction uses on
// one field. It drives the wildcard fallback and the merge signature: two
// groups may only be coalesced when their shapes match.
type fieldShape struct {
	hasIncl bool // at least one include (EQ) expression
	hasExcl bool // at least one exclude (EQ) expression
}

type groupBuilder struct {
	postings map[storage.TermKey][]uint64
	liveDocs map[uint64]struct{}
	seqs     map[uint64]uint64 // docID -> add sequence for tombstone checks
	shapes   map[string]fieldShape
	ranges   []rangePredicate
}

func newGroupBuilder() *groupBuilder {
	return &groupBuilder{
		postings: make(map[storage.TermKey][]uint64),
		liveDocs: make(map[uint64]struct{}),
		seqs:     make(map[uint64]uint64),
		shapes:   make(map[string]fieldShape),
	}
}

// Builder compiles documents into immutable Conjunction Segments. Each
// conjunction is sealed independently so DNF group boundaries are preserved.
type Builder struct {
	groups []*groupBuilder
}

func NewBuilder() *Builder { return &Builder{} }

// AddDocument indexes doc with add sequence 0 (never tombstoned).
func (b *Builder) AddDocument(doc *be.Document) error {
	return b.addDocument(doc, 0)
}

// addDocument indexes doc recording seq as its add sequence; tombstones with a
// greater sequence later mark these groups dead.
func (b *Builder) addDocument(doc *be.Document, seq uint64) error {
	if b == nil || doc == nil || len(doc.Cons) == 0 {
		return fmt.Errorf("%w: document is nil or empty", ErrUnsupportedExpression)
	}
	for _, conj := range doc.Cons {
		if conj == nil {
			return fmt.Errorf("%w: nil conjunction", ErrUnsupportedExpression)
		}
		group := newGroupBuilder()
		group.liveDocs[uint64(doc.ID)] = struct{}{}
		group.seqs[uint64(doc.ID)] = seq
		for field, expressions := range conj.Expressions {
			shape := group.shapes[string(field)]
			hasInclude := false
			hasExclude := false
			for _, expression := range expressions {
				if expression == nil {
					return fmt.Errorf("%w: field=%s nil expression", ErrUnsupportedExpression, field)
				}
				if expression.Operator != be.ValueOptEQ {
					left, right, err := parsePredicateRange(expression.Operator, expression.Value)
					if err != nil {
						return fmt.Errorf("%w: field=%s: %v", ErrUnsupportedExpression, field, err)
					}
					group.ranges = append(group.ranges, rangePredicate{field: string(field), left: left, right: right, incl: expression.Incl})
					continue
				}
				if expression.Incl {
					hasInclude = true
				} else {
					hasExclude = true
				}
				values, err := parser.ValuesToStrings(expression.Value)
				if err != nil {
					return fmt.Errorf("parse field %s: %w", field, err)
				}
				for _, value := range values {
					prefix := excludeValuePrefix
					if expression.Incl {
						prefix = includeValuePrefix
					}
					key := storage.TermKey{Field: string(field), Value: prefix + value}
					group.postings[key] = append(group.postings[key], uint64(doc.ID))
				}
			}
			shape.hasIncl = shape.hasIncl || hasInclude
			shape.hasExcl = shape.hasExcl || hasExclude
			group.shapes[string(field)] = shape
			if hasExclude && !hasInclude {
				key := storage.TermKey{Field: string(field), Value: wildcardValue}
				group.postings[key] = append(group.postings[key], uint64(doc.ID))
			}
		}
		b.groups = append(b.groups, group)
	}
	return nil
}

func (b *Builder) AddDocuments(docs ...*be.Document) error {
	for _, doc := range docs {
		if err := b.AddDocument(doc); err != nil {
			return err
		}
	}
	return nil
}

func sealGroup(group *groupBuilder) (*storage.Segment, error) {
	segmentBuilder := storage.NewSegmentBuilder()
	for id := range group.liveDocs {
		if err := segmentBuilder.AddLiveDoc(id); err != nil {
			return nil, err
		}
	}
	keys := make([]storage.TermKey, 0, len(group.postings))
	for key := range group.postings {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Field == keys[j].Field {
			return keys[i].Value < keys[j].Value
		}
		return keys[i].Field < keys[j].Field
	})
	for _, key := range keys {
		ids := append([]uint64(nil), group.postings[key]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err := segmentBuilder.AddPosting(key.Field, key.Value, ids); err != nil {
			return nil, err
		}
	}
	return segmentBuilder.Seal()
}

func (b *Builder) Seal() (*Index, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil builder", ErrUnsupportedExpression)
	}
	segments := make([]*storage.Segment, 0, len(b.groups))
	groups := make([]groupMeta, 0, len(b.groups))
	for _, group := range b.groups {
		segment, err := sealGroup(group)
		if err != nil {
			return nil, err
		}
		shapes := make(map[string]fieldShape, len(group.shapes))
		for field, shape := range group.shapes {
			shapes[field] = shape
		}
		seqs := make(map[uint64]uint64, len(group.seqs))
		for id, seq := range group.seqs {
			seqs[id] = seq
		}
		segments = append(segments, segment)
		groups = append(groups, groupMeta{shapes: shapes, ranges: append([]rangePredicate(nil), group.ranges...), seqs: seqs})
	}
	return &Index{segments: segments, groups: groups, planner: planner.New()}, nil
}

// groupMeta carries the conjunction-side metadata one Segment alone cannot
// express: expression shapes and range predicates per field, plus each doc's
// add sequence for tombstone (Delete/Update) checks.
type groupMeta struct {
	shapes map[string]fieldShape
	ranges []rangePredicate
	seqs   map[uint64]uint64 // docID -> add sequence
}

// dead reports whether tombstones mark id dead inside this group: a tombstone
// sequence greater than the doc's add sequence kills that version only, so a
// re-added (or updated) doc with a newer sequence stays alive.
func (m groupMeta) dead(id uint64, tomb *tombstoneSet) bool {
	if tomb == nil {
		return false
	}
	return tomb.dead(id, m.seqs[id])
}

// Index is an immutable exact-term DNF reader.
type Index struct {
	segments []*storage.Segment
	groups   []groupMeta
	planner  *planner.Planner
}

// Segment returns the first group Segment for compatibility with the single-
// conjunction integration API. Use Segments for the complete DNF view.
func (i *Index) Segment() *storage.Segment {
	if i == nil || len(i.segments) == 0 {
		return nil
	}
	return i.segments[0]
}

func (i *Index) Segments() []*storage.Segment {
	if i == nil {
		return nil
	}
	return append([]*storage.Segment(nil), i.segments...)
}

// Retrieve resolves an exact-term planner.Query against every conjunction
// segment. Numeric range predicates (GT/LT/Between) are not evaluated here:
// the planner matches exact terms only, so a range-only document can be
// missed (never over-matched) when no query value satisfies its range. Use
// RetrieveAssignments for full DNF evaluation including ranges.
func (i *Index) Retrieve(query planner.Query) ([]be.DocID, error) {
	return i.retrieveFiltered(query, nil)
}

// retrieveFiltered behaves like Retrieve but drops result ids whose group
// versions are marked dead by tomb.
func (i *Index) retrieveFiltered(query planner.Query, tomb *tombstoneSet) ([]be.DocID, error) {
	if i == nil {
		return nil, fmt.Errorf("segment index is nil")
	}
	// prefix a private copy: the caller's query may be reused against other
	// deltas and must not accumulate value prefixes
	terms := make([]planner.TermQuery, len(query.Terms))
	copy(terms, query.Terms)
	for idx := range terms {
		if terms[idx].Op == planner.OpNOT {
			terms[idx].Value = excludeValuePrefix + terms[idx].Value
		} else {
			terms[idx].Value = includeValuePrefix + terms[idx].Value
		}
	}
	seen := make(map[be.DocID]struct{})
	for segIdx, segment := range i.segments {
		plan, err := i.planner.Plan(segment, planner.Query{Terms: terms})
		if err != nil {
			return nil, err
		}
		ids, err := planner.Execute(segment, plan)
		if err != nil {
			return nil, err
		}
		meta := i.groups[segIdx]
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

func (i *Index) Explain(query planner.Query) ([]planner.ExplainResult, error) {
	if i == nil {
		return nil, fmt.Errorf("segment index is nil")
	}
	results := make([]planner.ExplainResult, 0, len(i.segments))
	for _, segment := range i.segments {
		plan, err := i.planner.Plan(segment, query)
		if err != nil {
			return nil, err
		}
		results = append(results, planner.Explain(segment, plan))
	}
	return results, nil
}
