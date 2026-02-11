//go:build property
// +build property

package be_indexer_test

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/echoface/be_indexer"
	_ "github.com/echoface/be_indexer/holder/ahoholder"
	_ "github.com/echoface/be_indexer/holder/rangeholder"
	"github.com/echoface/be_indexer/parser"
	"github.com/smartystreets/goconvey/convey"
)

// -----------------------------------------------------------------------------
// 1. Random Data Generators
// -----------------------------------------------------------------------------

type SchemaConfig struct {
	IntFields    []string
	StrFields    []string
	RangeFields  []string // Int fields using RangeHolder
	ACFields     []string // String fields using AC Holder
	ValuePoolInt []int64
	ValuePoolStr []string
}

func NewRandomSchema(seed int64) *SchemaConfig {
	rnd := rand.New(rand.NewSource(seed))
	s := &SchemaConfig{}

	// Generate Fields
	for i := 0; i < 2; i++ {
		s.IntFields = append(s.IntFields, fmt.Sprintf("int_%d", i))
	}
	for i := 0; i < 2; i++ {
		s.StrFields = append(s.StrFields, fmt.Sprintf("str_%d", i))
	}
	// Add specialized fields
	s.RangeFields = []string{"age", "score"} // Using RangeHolder
	s.ACFields = []string{"keywords", "content"}

	// Generate Value Pools to ensure collision/matches
	for i := 0; i < 50; i++ {
		s.ValuePoolInt = append(s.ValuePoolInt, int64(rnd.Intn(100)))
		s.ValuePoolStr = append(s.ValuePoolStr, fmt.Sprintf("val_%d", rnd.Intn(50)))
	}
	return s
}

func (s *SchemaConfig) RandInt(rnd *rand.Rand) int64 {
	return s.ValuePoolInt[rnd.Intn(len(s.ValuePoolInt))]
}

func (s *SchemaConfig) RandStr(rnd *rand.Rand) string {
	return s.ValuePoolStr[rnd.Intn(len(s.ValuePoolStr))]
}

func (s *SchemaConfig) GenDocument(id core.DocID, rnd *rand.Rand) *core.Document {
	doc := be_indexer.NewDocument(id)

	// Randomly decide number of conjunctions (1-3)
	conjCount := rnd.Intn(3) + 1

	for i := 0; i < conjCount; i++ {
		conj := be_indexer.NewConjunction()
		// Randomly add expressions for each field type

		// 1. Normal Int Fields (In/NotIn)
		for _, field := range s.IntFields {
			if rnd.Float32() < 0.3 {
				continue
			} // 30% skip
			values := []int64{s.RandInt(rnd)}
			if rnd.Float32() < 0.2 {
				values = append(values, s.RandInt(rnd))
			} // Multi-value

			if rnd.Float32() < 0.8 {
				conj.In(core.BEField(field), values)
			} else {
				conj.NotIn(core.BEField(field), values)
			}
		}

		// 2. Normal String Fields (In/NotIn)
		for _, field := range s.StrFields {
			if rnd.Float32() < 0.3 {
				continue
			}
			values := []string{s.RandStr(rnd)}
			if rnd.Float32() < 0.2 {
				values = append(values, s.RandStr(rnd))
			}

			if rnd.Float32() < 0.8 {
				conj.In(core.BEField(field), values)
			} else {
				conj.NotIn(core.BEField(field), values)
			}
		}

		// 3. Range Fields (RangeHolder supports EQ, LT, GT, Between)
		for _, field := range s.RangeFields {
			if rnd.Float32() < 0.4 {
				continue
			}
			op := rnd.Intn(4)
			v := s.RandInt(rnd)
			switch op {
			case 0: // EQ
				conj.In(core.BEField(field), []int64{v})
			case 1: // LT
				conj.LessThan(core.BEField(field), v)
			case 2: // GT
				conj.GreaterThan(core.BEField(field), v)
			case 3: // Between
				v2 := s.RandInt(rnd)
				if v > v2 {
					v, v2 = v2, v
				}
				conj.Between(core.BEField(field), v, v2)
			}
		}

		// 4. AC Fields (In - behaves as Contains)
		for _, field := range s.ACFields {
			if rnd.Float32() < 0.5 {
				continue
			}
			// Pick a few keywords
			kws := []string{s.RandStr(rnd)}
			conj.In(core.BEField(field), kws)
		}

		if len(conj.Predicates) > 0 {
			doc.AddConjunction(conj)
		}
	}
	return doc
}

func (s *SchemaConfig) GenAssignment(rnd *rand.Rand) core.Assignments {
	assign := core.Assignments{}

	// Assign Ints
	for _, f := range s.IntFields {
		assign[core.BEField(f)] = s.RandInt(rnd)
	}
	// Assign Strs
	for _, f := range s.StrFields {
		assign[core.BEField(f)] = s.RandStr(rnd)
	}
	// Assign Ranges
	for _, f := range s.RangeFields {
		assign[core.BEField(f)] = s.RandInt(rnd)
	}
	// Assign AC (Simulate a sentence containing keywords)
	for _, f := range s.ACFields {
		// Construct a sentence from pool
		var sb strings.Builder
		sb.WriteString("prefix_")
		sb.WriteString(s.RandStr(rnd)) // Keyword 1
		sb.WriteString("_mid_")
		if rnd.Float32() < 0.5 {
			sb.WriteString(s.RandStr(rnd)) // Keyword 2
		}
		sb.WriteString("_suffix")
		assign[core.BEField(f)] = sb.String()
	}
	return assign
}

// -----------------------------------------------------------------------------
// 2. Oracle Implementation
// -----------------------------------------------------------------------------

type Oracle struct {
	Docs []*core.Document
}

func NewOracle() *Oracle {
	return &Oracle{}
}

func (o *Oracle) AddDocument(doc *core.Document) {
	o.Docs = append(o.Docs, doc)
}

func (o *Oracle) Retrieve(assigns core.Assignments) core.DocIDList {
	var res core.DocIDList
	for _, doc := range o.Docs {
		if o.MatchDoc(doc, assigns) {
			res = append(res, doc.ID)
		}
	}
	sort.Sort(res)
	return res
}

func (o *Oracle) MatchDoc(doc *core.Document, assigns core.Assignments) bool {
	// Document matches if ANY of its conjunctions match (OR logic)
	if len(doc.Cons) == 0 {
		return false // Empty doc usually doesn't match? Or wild match? be_indexer logic: wildcard matches Z set.
		// If doc has no conjunctions, it's effectively empty. SUT won't index it usually.
	}

	for _, conj := range doc.Cons {
		if o.MatchConj(conj, assigns) {
			return true
		}
	}
	return false
}

func (o *Oracle) MatchConj(conj *core.Conjunction, assigns core.Assignments) bool {
	// Conjunction matches if ALL expressions match (AND logic)
	// Important: If a field is NOT in assignment?
	// be_indexer logic: If assignment doesn't provide field A, but Conjunction has condition on A:
	// - If A is logic "In" (include), it fails.
	// - If A is logic "NotIn" (exclude), it passes (conceptually, "undefined" is not in "values").
	// Wait, be_indexer strict mode might fail/skip. Assuming we provide all fields in assignment for now.

	for field, exprs := range conj.Predicates {
		val, hasAssign := assigns[field]

		// In boolean indexer, usually:
		// If the query doesn't provide a value for a field that is used in inclusion, it fails.
		// If the query doesn't provide a value for a field that is used in exclusion, it succeeds (unless logic dictates otherwise).
		// Let's implement strict check based on standard logic.

		for _, expr := range exprs {
			if !hasAssign {
				// Expression exists but no value provided.
				// In logic: "Age IN [18]" but user didn't tell us Age. MATCH FAIL.
				// NotIn logic: "Age NOT IN [18]" but user didn't tell us Age. MATCH PASS?
				// be_indexer implementation detail: It iterates over Assignments. If field not in assignment, it won't trigger lookup.
				// So if Conjunction has `age IN 18`, and we don't assign `age`, that Conjunction won't be picked up.
				// If Conjunction has ONLY `age NOT IN 18`, it's a "Z" entry (wildcard) + exclusion.
				// Correct Oracle logic:
				if expr.Incl {
					return false
				}
				continue
			}

			if !o.MatchExpr(expr, val) {
				return false
			}
		}
	}
	return true
}

func (o *Oracle) MatchExpr(expr *be_indexer.BoolValues, target interface{}) bool {
	// Handling types is tricky. Target is from Assignment (interface{}), Expr values are typed.

	// Helper to normalize to int64
	toInt64 := func(v interface{}) (int64, bool) {
		switch t := v.(type) {
		case int:
			return int64(t), true
		case int32:
			return int64(t), true
		case int64:
			return t, true
		}
		return 0, false
	}

	targetInt, isTargetInt := toInt64(target)
	_ = isTargetInt // used

	var match bool
	switch expr.Operator {
	case core.ValueOptEQ: // In or NotIn (depending on Incl)
		match = checkIn(expr.Value, target)

	case core.ValueOptLT:
		if !isTargetInt {
			return false
		}
		v, _ := parser.ParseIntegerNumber(expr.Value, true)
		match = targetInt < v

	case core.ValueOptGT:
		if !isTargetInt {
			return false
		}
		v, _ := parser.ParseIntegerNumber(expr.Value, true)
		match = targetInt > v

	case core.ValueOptBetween:
		if !isTargetInt {
			return false
		}
		// Values for between are usually range string or slice
		var rg *parser.RangeDesc
		if strVal, ok := expr.Value.(string); ok {
			rg = parser.NewRangeDesc(strVal)
		}

		if rg == nil {
			// Try slice [min, max]
			if s, ok := expr.Value.([]int64); ok && len(s) == 2 {
				// term_ext_range_holder.go uses NewRange(l, r) -> [l, r)
				// However, if we look at ParseBetween logic in holder:
				// It handles array [l, r] as min, max.
				// And then creates NewRange(l, r).
				// NewRange logic: if l==r { r++ } return &Range{l,r}
				// And ContainValue logic: v >= l && v < r
				// So internal implementation is [l, r).
				// BUT, parser logic usually interprets "10,20" as include both?
				// Let's assume standard behavior for now: [min, max).
				match = targetInt >= s[0] && targetInt < s[1]
			} else {
				match = false
			}
		} else {
			min, max, _ := rg.Values()
			match = targetInt >= min && targetInt < max
		}
	}

	if expr.Incl {
		return match
	}
	return !match
}

func checkIn(values core.Values, target interface{}) bool {
	// values is interface{}, could be []int64, []string, etc.
	switch list := values.(type) {
	case []int64:
		t, ok := target.(int64) // normalized target
		if !ok {
			// try casting target if it's int
			if ti, isInt := target.(int); isInt {
				t = int64(ti)
			} else {
				return false
			}
		}
		for _, v := range list {
			if v == t {
				return true
			}
		}
	case []string:
		t, ok := target.(string)
		if !ok {
			return false
		}
		for _, v := range list {
			// Special handling for AC logic: if list value is substring of target
			// But BoolValues doesn't know if it's AC field or not.
			// However, SUT `In` for AC field means "Target Contains Keyword".
			// But for standard string field, `In` means "Target Equals Value".
			// We need schema context here.
			// Simplification: In PropertyTest, we know which fields are AC.
			// But here we are generic.
			// Let's strict equals for now, and handle AC separately if possible?
			// Or just assume strict equality for `checkIn` and caller handles AC.
			if v == t {
				return true
			}
		}
	}
	return false
}

// Override MatchExpr for AC fields logic
func (o *Oracle) MatchAC(expr *be_indexer.BoolValues, target string) bool {
	// For AC, "In" means "Target contains any of keywords"
	keywords, ok := expr.Value.([]string)
	if !ok {
		return false
	}

	match := false
	if expr.Operator == core.ValueOptEQ {
		for _, kw := range keywords {
			if strings.Contains(target, kw) {
				match = true
				break
			}
		}
	}

	if expr.Incl {
		return match
	}
	return !match
}

func (o *Oracle) MatchExprWithSchema(field string, expr *be_indexer.BoolValues, target interface{}, schema *SchemaConfig) bool {
	// Check if AC Field
	for _, f := range schema.ACFields {
		if f == field {
			if s, ok := target.(string); ok {
				return o.MatchAC(expr, s)
			}
			return false
		}
	}
	return o.MatchExpr(expr, target)
}

// Updated MatchConj to use Schema
func (o *Oracle) MatchConjWithSchema(conj *core.Conjunction, assigns core.Assignments, schema *SchemaConfig) bool {
	for field, exprs := range conj.Predicates {
		val, hasAssign := assigns[field]
		for _, expr := range exprs {
			if !hasAssign {
				if expr.Incl {
					return false
				} // Missing include = fail
				continue // Missing exclude = pass
			}
			if !o.MatchExprWithSchema(string(field), expr, val, schema) {
				return false
			}
		}
	}
	return true
}

func (o *Oracle) MatchDocWithSchema(doc *core.Document, assigns core.Assignments, schema *SchemaConfig) bool {
	if len(doc.Cons) == 0 {
		return false
	}
	for _, conj := range doc.Cons {
		if o.MatchConjWithSchema(conj, assigns, schema) {
			return true
		}
	}
	return false
}

func (o *Oracle) RetrieveWithSchema(assigns core.Assignments, schema *SchemaConfig) core.DocIDList {
	var res core.DocIDList
	for _, doc := range o.Docs {
		if o.MatchDocWithSchema(doc, assigns, schema) {
			res = append(res, doc.ID)
		}
	}
	sort.Sort(res)
	return res
}

// -----------------------------------------------------------------------------
// 3. Property Test Runner
// -----------------------------------------------------------------------------

func TestProperty_BoolIndexCorrectness(t *testing.T) {
	// Configuration
	const (
		NumDocs    = 1000
		NumQueries = 200
		Seed       = 42
	)

	convey.Convey("Property-based Testing for Boolean Indexer", t, func() {
		rnd := rand.New(rand.NewSource(Seed))
		schema := NewRandomSchema(Seed)

		// 1. Setup SUT (System Under Test)
		builder := be_indexer.NewIndexerBuilder()

		// Register Holders
		// Default is auto-registered.
		// AC Holder
		for _, f := range schema.ACFields {
			builder.ConfigField(core.BEField(f), core.FieldOption{Container: be_indexer.HolderNameACMatcher})
		}
		// Range Holder
		for _, f := range schema.RangeFields {
			builder.ConfigField(core.BEField(f), core.FieldOption{Container: be_indexer.HolderNameExtendRange})
		}

		// 2. Setup Oracle
		oracle := NewOracle()

		// 3. Generate & Add Docs
		var docs []*core.Document
		for i := 1; i <= NumDocs; i++ {
			doc := schema.GenDocument(core.DocID(i), rnd)
			// Skip empty docs to avoid confusion (though system should handle them)
			if len(doc.Cons) == 0 {
				continue
			}

			docs = append(docs, doc)
			oracle.AddDocument(doc)
			err := builder.AddDocument(doc)
			convey.So(err, convey.ShouldBeNil)
		}

		// 4. Build Index
		indexer := builder.BuildIndex()
		convey.So(indexer, convey.ShouldNotBeNil)

		// 5. Generate & Execute Queries
		for i := 0; i < NumQueries; i++ {
			assign := schema.GenAssignment(rnd)

			// Oracle Result
			expected := oracle.RetrieveWithSchema(assign, schema)

			// SUT Result
			actual, err := indexer.Retrieve(assign)
			convey.So(err, convey.ShouldBeNil)
			sort.Sort(actual)

			// Verification
			if len(expected) != len(actual) {
				t.Logf("Mismatch Query: %+v", assign)
				t.Logf("Expected: %v", expected)
				t.Logf("Actual:   %v", actual)

				// Debug mismatch details: missing + extra docs
				expectedSet := make(map[core.DocID]struct{}, len(expected))
				for _, id := range expected {
					expectedSet[id] = struct{}{}
				}
				actualSet := make(map[core.DocID]struct{}, len(actual))
				for _, id := range actual {
					actualSet[id] = struct{}{}
				}
				for id := range expectedSet {
					if _, ok := actualSet[id]; !ok {
						// docs slice may skip empty docs; find by ID
						for _, d := range docs {
							if d.ID == id {
								t.Logf("Missing Doc %d: %s", id, d.JSONString())
								break
							}
						}
					}
				}
				for id := range actualSet {
					if _, ok := expectedSet[id]; !ok {
						for _, d := range docs {
							if d.ID == id {
								t.Logf("Extra Doc %d: %s", id, d.JSONString())
								break
							}
						}
					}
				}
			}
			convey.So(actual, convey.ShouldResemble, expected)
		}
	})
}
