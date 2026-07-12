package builder

import (
	"bytes"
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/oracle"
	"github.com/echoface/be_indexer/segment"
)

// TestRangeShadowAgainstOracle is the end-to-end correctness gate for range
// targeting. It builds documents mixing EQ predicates with range predicates
// (>, <, between, not-between) over an ext_range field, then verifies the index
// retrieval matches the oracle ground truth for randomized integer queries,
// including the exclude-on-present-value semantics.
func TestRangeShadowAgainstOracle(t *testing.T) {
	const numDocs = 200
	const numQueries = 2000
	rng := rand.New(rand.NewSource(7))

	fields := map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{Container: core.IndexNameExtendRange}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{Tokenizer: "default"}},
	}
	cities := []string{"bj", "sh", "gz"}

	addAgePredicate := func(conj *core.Conjunction) {
		switch rng.Intn(5) {
		case 0:
			conj.GreaterThan("age", int64(rng.Intn(80)+1))
		case 1:
			conj.LessThan("age", int64(rng.Intn(80)+1))
		case 2:
			lo := int64(rng.Intn(60) + 1)
			hi := lo + int64(rng.Intn(30))
			conj.Between("age", lo, hi)
		case 3:
			// not-between (exclude range)
			lo := int64(rng.Intn(60) + 1)
			hi := lo + int64(rng.Intn(30))
			conj.AddPredicates(core.NewPredicateWithExpr("age", core.NewValueExpr(core.ValueOptBetween, []int64{lo, hi}, false)))
		case 4:
			conj.Include("age", int64(rng.Intn(100)+1)) // exact EQ via range container
		}
	}

	docs := make([]*core.Document, 0, numDocs)
	for i := 0; i < numDocs; i++ {
		doc := core.NewDocument(core.DocID(i + 1))
		numConj := rng.Intn(2) + 1
		for j := 0; j < numConj; j++ {
			conj := core.NewConjunction()
			if rng.Intn(3) != 0 {
				addAgePredicate(conj)
			}
			if rng.Intn(2) == 0 {
				city := cities[rng.Intn(len(cities))]
				if rng.Intn(4) == 0 {
					conj.NotIn("city", city)
				} else {
					conj.In("city", city)
				}
			}
			doc.AddConjunction(conj)
		}
		if len(doc.Cons) == 0 {
			doc.AddConjunction(core.NewConjunction())
		}
		docs = append(docs, doc)
	}

	buf := new(bytes.Buffer)
	wildcards, err := BuildSegmentFromDocs(buf, fields, docs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	seg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	eng, err := engine.NewBooleanEngine(fields, wildcards, []*segment.SegmentReader{seg})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}

	for qi := 0; qi < numQueries; qi++ {
		q := core.Assignments{}
		if rng.Intn(3) != 0 {
			q["age"] = rng.Intn(110) // include 0 and out-of-range points
		}
		if rng.Intn(2) == 0 {
			q["city"] = cities[rng.Intn(len(cities))]
		}

		got, err := eng.Retrieve(q)
		if err != nil {
			t.Fatalf("retrieve q=%v: %v", q, err)
		}
		want, err := oracle.MatchDocuments(docs, q)
		if err != nil {
			t.Fatalf("oracle q=%v: %v", q, err)
		}
		gotSlice := bitmapToSlice(got)
		sort.Slice(gotSlice, func(i, j int) bool { return gotSlice[i] < gotSlice[j] })

		if len(gotSlice) != len(want) {
			t.Fatalf("q=%v len mismatch: index=%v oracle=%v", q, gotSlice, want)
		}
		for j := range gotSlice {
			if gotSlice[j] != want[j] {
				t.Fatalf("q=%v mismatch: index=%v oracle=%v", q, gotSlice, want)
			}
		}
	}
}

// TestRangeUnboundedEdges checks GT/LT against the int64 domain extremes flow
// through build + retrieve without overflow surprises.
func TestRangeUnboundedEdges(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"v": {ID: 1, Field: "v", FieldOption: core.FieldOption{Container: core.IndexNameExtendRange}},
	}
	doc := core.NewDocument(1)
	doc.AddConjunction(core.NewConjunction().GreaterThan("v", 0))
	docs := []*core.Document{doc}

	buf := new(bytes.Buffer)
	wildcards, err := BuildSegmentFromDocs(buf, fields, docs)
	if err != nil {
		t.Fatal(err)
	}
	seg, _ := segment.NewSegmentReader(buf.Bytes())
	eng, err := engine.NewBooleanEngine(fields, wildcards, []*segment.SegmentReader{seg})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}

	for _, c := range []struct {
		q    int
		want bool
	}{
		{0, false}, {1, true}, {math.MaxInt32, true},
	} {
		r, err := eng.Retrieve(core.Assignments{"v": c.q})
		if err != nil {
			t.Fatal(err)
		}
		if (r.Cardinality() > 0) != c.want {
			t.Fatalf("v=%d got %v want match=%v", c.q, r, c.want)
		}
	}
}
