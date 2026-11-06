package be_indexer_test

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
)

type phase0Rule struct {
	id    be.DocID
	terms map[be.BEField][]int
	excl  map[be.BEField]bool
}

var phase0Fields = []be.BEField{"A", "B", "C", "D"}

func newPhase0Rule(id be.DocID, rng *rand.Rand) (*phase0Rule, *be.Document) {
	rule := &phase0Rule{
		id:    id,
		terms: make(map[be.BEField][]int),
		excl:  make(map[be.BEField]bool),
	}
	doc := be.NewDocument(id)
	conj := be.NewConjunction()
	for _, field := range phase0Fields {
		// Keep some fields absent so wildcard semantics are covered.
		if rng.Intn(4) == 0 {
			continue
		}
		values := []int{rng.Intn(16), rng.Intn(16)}
		values = distinctInts(values)
		rule.terms[field] = values
		rule.excl[field] = rng.Intn(2) == 0
		if rule.excl[field] {
			conj.Exclude(field, be.NewIntValues(values[0], values[1:]...))
		} else {
			conj.Include(field, be.NewIntValues(values[0], values[1:]...))
		}
	}
	// A zero-expression conjunction is a wildcard rule.
	doc.AddConjunction(conj)
	return rule, doc
}

func distinctInts(values []int) []int {
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func phase0Assignments(rng *rand.Rand) be.Assignments {
	assignments := make(be.Assignments, len(phase0Fields))
	for _, field := range phase0Fields {
		values := []int{rng.Intn(16), rng.Intn(16)}
		assignments[field] = be.NewIntValues(values[0], values[1:]...)
	}
	return assignments
}

func phase0Matches(rule *phase0Rule, assignments be.Assignments) bool {
	for field, values := range rule.terms {
		query, ok := assignments[field]
		if !ok {
			return false
		}
		queryValues, ok := query.([]int)
		if !ok {
			return false
		}
		hit := false
		for _, value := range values {
			for _, queryValue := range queryValues {
				if value == queryValue {
					hit = true
					break
				}
			}
			if hit {
				break
			}
		}
		if rule.excl[field] && hit {
			return false
		}
		if !rule.excl[field] && !hit {
			return false
		}
	}
	return true
}

func buildPhase0Indexes(t *testing.T, docs []*be.Document) (be.BEIndex, *roaringidx.IvtBEIndexer) {
	t.Helper()

	defaultBuilder := be.NewIndexerBuilder()
	roaringBuilder := roaringidx.NewIndexerBuilder()
	for _, field := range phase0Fields {
		defaultBuilder.ConfigField(field, be.FieldOption{Container: be.HolderNameDefault})
		if err := roaringBuilder.ConfigureField(string(field), roaringidx.FieldSetting{
			Container: roaringidx.ContainerNameDefault,
			Parser:    parser.NewNumberParser(),
		}); err != nil {
			t.Fatalf("configure roaring field %s: %v", field, err)
		}
	}
	if err := defaultBuilder.AddDocument(docs...); err != nil {
		t.Fatalf("add documents to default index: %v", err)
	}
	if err := roaringBuilder.AddDocuments(docs...); err != nil {
		t.Fatalf("add documents to roaring index: %v", err)
	}
	return defaultBuilder.BuildIndex(), mustBuildRoaring(t, roaringBuilder)
}

func mustBuildRoaring(t *testing.T, builder *roaringidx.IvtBEIndexerBuilder) *roaringidx.IvtBEIndexer {
	t.Helper()
	index, err := builder.BuildIndexer()
	if err != nil {
		t.Fatalf("build roaring index: %v", err)
	}
	return index
}

func sortedDocIDs(ids be.DocIDList) []be.DocID {
	result := append([]be.DocID(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func roaringDocIDs(ids []uint64) []be.DocID {
	result := make([]be.DocID, 0, len(ids))
	for _, id := range ids {
		result = append(result, be.DocID(id))
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func TestPhase0_BooleanContract(t *testing.T) {
	rng := rand.New(rand.NewSource(phase0Seed))
	const docCount = 160
	rules := make([]*phase0Rule, 0, docCount)
	docs := make([]*be.Document, 0, docCount)
	for i := 0; i < docCount; i++ {
		rule, doc := newPhase0Rule(be.DocID(i+1), rng)
		rules = append(rules, rule)
		docs = append(docs, doc)
	}

	defaultIndex, roaringIndex := buildPhase0Indexes(t, docs)
	for queryNumber := 0; queryNumber < 300; queryNumber++ {
		assignments := phase0Assignments(rng)
		expected := make([]be.DocID, 0)
		for _, rule := range rules {
			if phase0Matches(rule, assignments) {
				expected = append(expected, rule.id)
			}
		}
		sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })

		gotDefault, err := defaultIndex.Retrieve(assignments)
		if err != nil {
			t.Fatalf("query %d default index: %v", queryNumber, err)
		}
		gotRoaring, err := roaringidx.NewScanner(roaringIndex).Retrieve(assignments)
		if err != nil {
			t.Fatalf("query %d roaring index: %v", queryNumber, err)
		}
		if got := sortedDocIDs(gotDefault); !reflect.DeepEqual(got, expected) {
			t.Fatalf("query %d default mismatch: got %v want %v assignments=%v", queryNumber, got, expected, assignments)
		}
		if got := roaringDocIDs(gotRoaring); !reflect.DeepEqual(got, expected) {
			t.Fatalf("query %d roaring mismatch: got %v want %v assignments=%v", queryNumber, got, expected, assignments)
		}
		if !reflect.DeepEqual(sortedDocIDs(gotDefault), roaringDocIDs(gotRoaring)) {
			t.Fatalf("query %d implementation mismatch: default=%v roaring=%v", queryNumber, gotDefault, gotRoaring)
		}
	}
}

const phase0Seed int64 = 20261104

func TestPhase0_BoundaryContract(t *testing.T) {
	t.Run("empty assignments returns wildcard and fully-negative matches", func(t *testing.T) {
		builder := be.NewIndexerBuilder()
		builder.ConfigField("A", be.FieldOption{Container: be.HolderNameDefault})
		wildcard := be.NewDocument(1).AddConjunction(be.NewConjunction())
		negative := be.NewDocument(2).AddConjunction(be.NewConjunction().NotIn("A", be.NewIntValues(7)))
		if err := builder.AddDocument(wildcard, negative); err != nil {
			t.Fatal(err)
		}
		index := builder.BuildIndex()
		got, err := index.Retrieve(be.Assignments{})
		if err != nil {
			t.Fatal(err)
		}
		want := []be.DocID{1, 2}
		if !reflect.DeepEqual(sortedDocIDs(got), want) {
			t.Fatalf("got %v want %v", got, want)
		}
	})

	t.Run("unknown query field does not create a match", func(t *testing.T) {
		builder := be.NewIndexerBuilder()
		builder.ConfigField("A", be.FieldOption{Container: be.HolderNameDefault})
		if err := builder.AddDocument(be.NewDocument(1).AddConjunction(be.NewConjunction().In("A", be.NewIntValues(1)))); err != nil {
			t.Fatal(err)
		}
		got, err := builder.BuildIndex().Retrieve(be.Assignments{"unknown": be.NewIntValues(1)})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want no matches", got)
		}
	})

	t.Run("empty document is rejected", func(t *testing.T) {
		builder := be.NewIndexerBuilder()
		if err := builder.AddDocument(be.NewDocument(1)); err == nil {
			t.Fatal("expected empty document error")
		}
	})
}
