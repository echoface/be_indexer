package engine_test

import (
	"bytes"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

// --- helpers ---

func testFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{}},
		"tag":  {ID: 3, Field: "tag", FieldOption: core.FieldOption{}},
	}
}

func simpleFields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}},
		"b": {ID: 2, Field: "b", FieldOption: core.FieldOption{Encoder: "number"}},
		"c": {ID: 3, Field: "c", FieldOption: core.FieldOption{Encoder: "number"}},
	}
}

func buildSingleEngine(t *testing.T, fields map[core.BEField]*core.FieldMeta, docs []*core.Document) *engine.BooleanEngine {
	t.Helper()
	buf := new(bytes.Buffer)
	err := builder.BuildSegmentFromDocs(buf, fields, docs, builder.BuildSegmentFromDocsOptions{})
	if err != nil {
		t.Fatalf("BuildSegmentFromDocs failed: %v", err)
	}
	seg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("NewSegmentReader failed: %v", err)
	}
	eng, err := engine.NewBooleanEngine(fields, []*segment.SegmentReader{seg})
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}
	return eng
}

// buildMultiEngine creates an engine with docs split across segments of chunkSize.
func buildMultiEngine(t *testing.T, fields map[core.BEField]*core.FieldMeta, docs []*core.Document, chunkSize int) *engine.BooleanEngine {
	t.Helper()
	var bufs []*bytes.Buffer
	_, err := builder.BuildSegmentsFromDocs(
		func(_ int) (io.Writer, error) {
			b := new(bytes.Buffer)
			bufs = append(bufs, b)
			return b, nil
		},
		fields, docs,
		builder.BuildSegmentsFromDocsOptions{MaxDocsPerSegment: chunkSize},
	)
	if err != nil {
		t.Fatalf("BuildSegmentsFromDocs failed: %v", err)
	}
	segs := make([]*segment.SegmentReader, 0, len(bufs))
	for _, b := range bufs {
		sr, err := segment.NewSegmentReader(b.Bytes())
		if err != nil {
			t.Fatalf("NewSegmentReader failed: %v", err)
		}
		segs = append(segs, sr)
	}
	eng, err := engine.NewBooleanEngine(fields, segs)
	if err != nil {
		t.Fatalf("NewBooleanEngine failed: %v", err)
	}
	return eng
}

// --- TestResultCollector ---
type testResultCollector struct {
	ids []core.DocID
}

func (c *testResultCollector) Add(id core.DocID, _ core.ConjID) { c.ids = append(c.ids, id) }

func bitmapToSlice(b *core.BitmapDocSet) core.DocIDList {
	if b == nil {
		return nil
	}
	var ids core.DocIDList
	b.ForEach(func(id core.DocID) {
		ids = append(ids, id)
	})
	return ids
}

// --- TestObserver ---
type testObserver struct {
	startCount int
	endCount   int
	matchCount int
	exclCount  int
	cursorInit int
}

func (o *testObserver) OnRetrieveStart(*core.RetrieveContext)   { o.startCount++ }
func (o *testObserver) OnRetrieveEnd(*core.RetrieveContext)     { o.endCount++ }
func (o *testObserver) OnMatch(docID core.DocID, _ core.ConjID) { o.matchCount++ }
func (o *testObserver) OnExcludeSkip(core.DocID)                { o.exclCount++ }
func (o *testObserver) OnCursorInit(int)                        { o.cursorInit++ }

// ==========================================================================
func TestEngine_BasicQuery(t *testing.T) {

	fields := map[core.BEField]*core.FieldMeta{
		"age":  {ID: 1, Field: "age", FieldOption: core.FieldOption{Encoder: "number"}},
		"city": {ID: 2, Field: "city", FieldOption: core.FieldOption{}},
	}
	docs := []*core.Document{
		// Doc 1: K=2 (age + city)
		core.NewDocument(1).AddConjunction(
			core.NewConjunction().In("age", 25).In("city", "bj"),
		),
		// Doc 2: K=1 (age only)
		core.NewDocument(2).AddConjunction(
			core.NewConjunction().In("age", 25),
		),
		// Doc 3: K=1 (age only, different value)
		core.NewDocument(3).AddConjunction(
			core.NewConjunction().In("age", 30),
		),
		// Doc 4: K=2 (age + city)
		core.NewDocument(4).AddConjunction(
			core.NewConjunction().In("age", 25).In("city", "gz"),
		),
	}
	eng := buildSingleEngine(t, fields, docs)

	checks := []struct {
		q    core.Assignments
		want []core.DocID
	}{
		{q: core.Assignments{"age": 25}, want: []core.DocID{2}},
		{q: core.Assignments{"age": 25, "city": "bj"}, want: []core.DocID{1, 2}},
		{q: core.Assignments{"age": 30}, want: []core.DocID{3}},
		{q: core.Assignments{"age": 99}, want: nil},
		{q: core.Assignments{"xxx": 1}, want: nil},
		{q: core.Assignments{"age": 30, "city": "bj"}, want: []core.DocID{3}},
	}
	for _, c := range checks {
		b, err := eng.Retrieve(c.q)
		if err != nil {
			t.Fatalf("Retrieve(%v) failed: %v", c.q, err)
		}
		res := bitmapToSlice(b)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		if len(res) != len(c.want) {
			t.Fatalf("Retrieve(%v): got %v, want %v", c.q, res, c.want)
		}
		for i := range res {
			if res[i] != c.want[i] {
				t.Fatalf("Retrieve(%v): got %v, want %v", c.q, res, c.want)
			}
		}
	}

}
func TestEngine_EmptyQuery(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("a", 99)), // K=0
	}
	eng := buildSingleEngine(t, fields, docs)

	b, err := eng.Retrieve(core.Assignments{})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res := bitmapToSlice(b)
	if len(res) != 1 || res[0] != 2 {
		t.Errorf("empty query: want [2], got %v", res)
	}
}

// ==========================================================================
// 2. Multi-segment consistency
// ==========================================================================

func TestEngine_MultiSegment(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 30)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 30)),
	}

	eng1 := buildSingleEngine(t, fields, docs)
	eng2 := buildMultiEngine(t, fields, docs, 2)

	queries := []core.Assignments{
		{"a": 25},
		{"a": 30},
		{"a": 99},
	}
	for i, q := range queries {
		br1, _ := eng1.Retrieve(q)
		r1 := bitmapToSlice(br1)
		br2, _ := eng2.Retrieve(q)
		r2 := bitmapToSlice(br2)
		sort.Slice(r1, func(i, j int) bool { return r1[i] < r1[j] })
		sort.Slice(r2, func(i, j int) bool { return r2[i] < r2[j] })
		if len(r1) != len(r2) {
			t.Fatalf("q[%d]: single=%v multi=%v", i, r1, r2)
		}
		for j := range r1 {
			if r1[j] != r2[j] {
				t.Fatalf("q[%d] elem[%d]: %d vs %d", i, j, r1[j], r2[j])
			}
		}
	}
}

// ==========================================================================
// 3. Exclude (NOT IN) short-circuit
// ==========================================================================

func TestEngine_ExcludeShortCircuit(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25).NotIn("b", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("b", 1)), // K=0 wildcard
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 25)),
	}
	eng := buildSingleEngine(t, fields, docs)

	// q={a:25, b:1} → exclude b=1 should fire for docs 1,2; doc 3 matches
	b, err := eng.Retrieve(core.Assignments{"a": 25, "b": 1})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res := bitmapToSlice(b)
	if len(res) != 1 || res[0] != 3 {
		t.Errorf("q={a:25,b:1}: should match only doc 3, got %v", res)
	}

	// q={a:25, b:2} → no exclude → all 3 match
	b, err = eng.Retrieve(core.Assignments{"a": 25, "b": 2})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res = bitmapToSlice(b)
	if len(res) != 3 {
		t.Errorf("q={a:25,b:2}: should match 3 docs, got %v", res)
	}
}

// ==========================================================================
// 4. K=0 wildcard entries
// ==========================================================================
func TestEngine_K0Wildcard(t *testing.T) {

	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().NotIn("a", 1)), // pure exclude, K=0
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 25).NotIn("b", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 25)), // K=1
	}
	eng := buildSingleEngine(t, fields, docs)

	checks := []struct {
		q    core.Assignments
		want int
	}{
		{q: core.Assignments{"a": 1}, want: 0},          // doc 1 excluded by a=1; doc 2,3 a≠25
		{q: core.Assignments{"a": 25}, want: 3},         // docs 1+2+3
		{q: core.Assignments{"a": 25, "b": 1}, want: 2}, // doc 2 excluded by b=1; doc 1+3 match
		{q: core.Assignments{"b": 2}, want: 1},          // only doc 1 (K=0 wildcard)
		{q: core.Assignments{}, want: 1},                // only doc 1 (K=0 wildcard)
	}
	for _, c := range checks {
		b, err := eng.Retrieve(c.q)
		if err != nil {
			t.Fatalf("Retrieve(%v) failed: %v", c.q, err)
		}
		res := bitmapToSlice(b)
		if len(res) != c.want {
			t.Errorf("Retrieve(%v): got %d want %d: %v", c.q, len(res), c.want, res)
		}
	}
}

// ==========================================================================
// 5. Observer hooks
// ==========================================================================

func TestEngine_ObserverStartEnd(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25).NotIn("b", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 30)),
	}
	eng := buildSingleEngine(t, fields, docs)

	obs := &testObserver{}
	_, err := eng.Retrieve(
		core.Assignments{"a": 25, "b": 1},
		func(ctx *core.RetrieveContext) { ctx.Observer = obs },
	)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if obs.startCount != 1 {
		t.Errorf("OnRetrieveStart called %d times, want 1", obs.startCount)
	}
	if obs.endCount != 1 {
		t.Errorf("OnRetrieveEnd called %d times, want 1", obs.endCount)
	}
	if obs.cursorInit == 0 {
		t.Errorf("OnCursorInit not called")
	}
}

func TestEngine_ObserverMatchCount(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 30)),
	}
	eng := buildSingleEngine(t, fields, docs)

	obs := &testObserver{}
	b, err := eng.Retrieve(
		core.Assignments{"a": 25},
		func(ctx *core.RetrieveContext) { ctx.Observer = obs },
	)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res := bitmapToSlice(b)
	if len(res) != 2 {
		t.Errorf("expected 2 results, got %v", res)
	}
	if obs.matchCount != 2 {
		t.Errorf("OnMatch called %d times, want 2", obs.matchCount)
	}
}

// ==========================================================================
// 6. LiveDocs filtering
// ==========================================================================

func TestEngine_LiveDocs(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 25)),
	}
	eng := buildSingleEngine(t, fields, docs)

	res, _ := eng.Retrieve(core.Assignments{"a": 25})
	resSlice := bitmapToSlice(res)
	if len(resSlice) != 3 {
		t.Fatalf("without LiveDocs: want 3, got %v", resSlice)
	}

	ld := core.NewLiveDocs()
	ld.MarkDeleted(2)
	eng.SetLiveDocs(ld)

	res, _ = eng.Retrieve(core.Assignments{"a": 25})
	resSlice = bitmapToSlice(res)
	if len(resSlice) != 2 {
		t.Fatalf("with LiveDocs: want 2, got %v", resSlice)
	}
	for _, id := range resSlice {
		if id == 2 {
			t.Errorf("doc 2 should be filtered")
		}
	}
}

func TestEngine_LiveDocsAllDeleted(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
	}
	eng := buildSingleEngine(t, fields, docs)

	ld := core.NewLiveDocs()
	ld.MarkDeleted(1)
	eng.SetLiveDocs(ld)

	res, _ := eng.Retrieve(core.Assignments{"a": 25})
	resSlice := bitmapToSlice(res)
	if len(resSlice) != 0 {
		t.Errorf("all deleted: expected 0, got %v", resSlice)
	}
}

// ==========================================================================
// 7. Multi K-level matching
// ==========================================================================

func TestEngine_MultiKLevels(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"a": {ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}},
		"b": {ID: 2, Field: "b", FieldOption: core.FieldOption{Encoder: "number"}},
		"c": {ID: 3, Field: "c", FieldOption: core.FieldOption{Encoder: "number"}},
	}
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1).In("b", 2).In("c", 3)), // K=3
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1).In("b", 2)),            // K=2
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 1)),                       // K=1
		core.NewDocument(4).AddConjunction(core.NewConjunction().NotIn("a", 99)),                   // K=0
	}
	eng := buildSingleEngine(t, fields, docs)

	check := func(q core.Assignments, want []core.DocID) {
		t.Helper()
		b, err := eng.Retrieve(q)
		if err != nil {
			t.Fatalf("Retrieve(%v) failed: %v", q, err)
		}
		res := bitmapToSlice(b)
		sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
		if len(res) != len(want) {
			t.Fatalf("Retrieve(%v): got %v, want %v", q, res, want)
		}
		for i := range res {
			if res[i] != want[i] {
				t.Fatalf("Retrieve(%v): got %v, want %v", q, res, want)
			}
		}
	}

	check(core.Assignments{"a": 1, "b": 2, "c": 3}, []core.DocID{1, 2, 3, 4}) // K=3+2+1+0
	check(core.Assignments{"a": 1, "b": 2}, []core.DocID{2, 3, 4})            // K=2+1+0 (doc 1 has K=3, needs 3 fields)
	check(core.Assignments{"a": 1}, []core.DocID{3, 4})                       // K=1+0 (doc 1 needs 3, doc 2 needs 2)
	check(core.Assignments{"a": 99}, nil)                                     // doc 4 NotIn(a,99) excludes
}

// ==========================================================================
// 8. RetrieveWithCollector
// ==========================================================================

func TestEngine_RetrieveWithCollector(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 30)),
	}
	eng := buildSingleEngine(t, fields, docs)

	c := &testResultCollector{}
	err := eng.RetrieveWithCollector(core.Assignments{"a": 25}, c)
	if err != nil {
		t.Fatalf("RetrieveWithCollector failed: %v", err)
	}
	if len(c.ids) != 1 || c.ids[0] != 1 {
		t.Errorf("expected [1], got %v", c.ids)
	}
}

// ==========================================================================
// 9. DumpIndexInfo
// ==========================================================================

func TestEngine_DumpIndexInfo(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
	}
	eng := buildSingleEngine(t, fields, docs)

	var sb strings.Builder
	eng.DumpIndexInfo(&sb)
	if sb.Len() == 0 {
		t.Error("DumpIndexInfo should write output")
	}
}

func TestEngine_DumpIndexInfoMultiSegment(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().NotIn("a", 99)),
	}
	eng := buildMultiEngine(t, fields, docs, 1)

	var sb strings.Builder
	eng.DumpIndexInfo(&sb)
	if sb.Len() == 0 {
		t.Error("DumpIndexInfo should write output for multi-segment")
	}
}

// ==========================================================================
// 10. Pooled collector reuse
// ==========================================================================

func TestEngine_PoolReuse(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 25)),
	}
	eng := buildSingleEngine(t, fields, docs)

	for i := 0; i < 5; i++ {
		b, err := eng.Retrieve(core.Assignments{"a": 25})
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		res := bitmapToSlice(b)
		if len(res) != 1 {
			t.Errorf("iteration %d: want 1, got %v", i, res)
		}
	}
}

// ==========================================================================
// 11. Overlapping queries (multiple query values per field)
// ==========================================================================

func TestEngine_MultiValueQuery(t *testing.T) {
	fields := map[core.BEField]*core.FieldMeta{
		"age": {ID: 1, Field: "age", FieldOption: core.FieldOption{Encoder: "number"}},
	}
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 25)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("age", 30)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("age", 35)),
	}
	eng := buildSingleEngine(t, fields, docs)

	// Query with multiple values
	b, err := eng.Retrieve(core.Assignments{"age": []int{25, 30}})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res := bitmapToSlice(b)
	sort.Slice(res, func(i, j int) bool { return res[i] < res[j] })
	if len(res) != 2 || res[0] != 1 || res[1] != 2 {
		t.Errorf("multi-value query: want [1 2], got %v", res)
	}
}

// ==========================================================================
// 12. Mixed include/exclude across multiple conjunctions
// ==========================================================================

func TestEngine_MultiConjunctionDoc(t *testing.T) {
	fields := simpleFields()
	docs := []*core.Document{
		core.NewDocument(1).AddConjunctions(
			core.NewConjunction().In("a", 25).NotIn("b", 1), // matches a=25,b≠1
			core.NewConjunction().In("a", 30),               // matches a=30
		),
		core.NewDocument(2).AddConjunctions(
			core.NewConjunction().In("a", 25), // matches a=25
		),
	}
	eng := buildSingleEngine(t, fields, docs)

	// q={a:25, b:1} → doc 1 conj1 excluded, but doc 1 conj2? Has a=30, not a=25 → fail.
	// So only doc 2 matches
	b, err := eng.Retrieve(core.Assignments{"a": 25, "b": 1})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res := bitmapToSlice(b)
	if len(res) != 1 || res[0] != 2 {
		t.Errorf("exclude first conj: want [2], got %v", res)
	}

	// q={a:30, b:2} → doc 1 conj1 (a=25≠30 → fail), conj2 (a=30 → match)
	// doc 2 (a=25≠30 → fail)
	b, err = eng.Retrieve(core.Assignments{"a": 30, "b": 2})
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	res = bitmapToSlice(b)
	if len(res) != 1 || res[0] != 1 {
		t.Errorf("second conj matches: want [1], got %v", res)
	}
}

var benchmarkCompositeIDs core.DocIDList

func BenchmarkCompositeEngineFullVsFullDelta(b *testing.B) {
	fields := simpleFields()
	fullDocs := makeBenchmarkDocs(10_000, 1, 0)
	deltaDocs := makeBenchmarkDocs(500, 1, 1_000)
	fullEngine := buildBenchmarkEngine(b, fields, fullDocs)
	deltaEngine := buildBenchmarkEngine(b, fields, deltaDocs)

	changedDocs := core.NewBitmapDocSet()
	for i := range deltaDocs {
		changedDocs.Add(core.DocID(i + 1))
	}
	query := core.Assignments{"a": 42, "b": 3}
	benchmarks := []struct {
		name     string
		snapshot *engine.IndexSnapshot
	}{
		{
			name: "full_only",
			snapshot: &engine.IndexSnapshot{
				Generation: 1,
				FullEngine: fullEngine,
			},
		},
		{
			name: "full_delta",
			snapshot: &engine.IndexSnapshot{
				Generation:   2,
				FullEngine:   fullEngine,
				DeltaEngines: []*engine.BooleanEngine{deltaEngine},
				ChangedDocs:  changedDocs,
				DeletedDocs:  core.NewBitmapDocSet(),
			},
		},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			ce := engine.NewCompositeEngine(bm.snapshot)
			b.ReportAllocs()
			b.ResetTimer()
			var ids core.DocIDList
			for i := 0; i < b.N; i++ {
				var err error
				bm, err := ce.Retrieve(query)
				if err != nil {
					b.Fatal(err)
				}
				ids = bitmapToSlice(bm)
			}
			benchmarkCompositeIDs = ids
		})
	}
}

func makeBenchmarkDocs(n int, firstDocID core.DocID, valueOffset int) []*core.Document {
	docs := make([]*core.Document, 0, n)
	for i := 0; i < n; i++ {
		docID := firstDocID + core.DocID(i)
		docs = append(docs, core.NewDocument(docID).AddConjunction(
			core.NewConjunction().In("a", (i+valueOffset)%128).In("b", (i/128)%8),
		))
	}
	return docs
}

func buildBenchmarkEngine(b *testing.B, fields map[core.BEField]*core.FieldMeta, docs []*core.Document) *engine.BooleanEngine {
	b.Helper()
	buf := new(bytes.Buffer)
	err := builder.BuildSegmentFromDocs(buf, fields, docs, builder.BuildSegmentFromDocsOptions{})
	if err != nil {
		b.Fatalf("BuildSegmentFromDocs failed: %v", err)
	}
	seg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		b.Fatalf("NewSegmentReader failed: %v", err)
	}
	eng, err := engine.NewBooleanEngine(fields, []*segment.SegmentReader{seg})
	if err != nil {
		b.Fatalf("NewBooleanEngine failed: %v", err)
	}
	return eng
}
