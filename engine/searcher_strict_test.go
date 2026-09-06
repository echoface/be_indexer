package engine_test

import (
	"bytes"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

func buildStrictEngine(t *testing.T) *engine.BooleanEngine {
	t.Helper()
	fields := map[core.BEField]*core.FieldMeta{
		"age": {ID: 1, Field: "age", FieldOption: core.FieldOption{IndexType: core.IndexNameDefault, Encoder: "number"}},
	}
	docs := []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("age", 18)),
	}
	buf := new(bytes.Buffer)
	err := builder.BuildSegmentFromDocs(buf, fields, docs, builder.BuildSegmentFromDocsOptions{})
	if err != nil {
		t.Fatalf("BuildSegmentFromDocs: %v", err)
	}
	seg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("NewSegmentReader: %v", err)
	}
	eng, err := engine.NewBooleanEngine(fields, []*segment.SegmentReader{seg})
	if err != nil {
		t.Fatalf("NewBooleanEngine: %v", err)
	}
	return eng
}

// fieldErrObserver captures OnFieldError callbacks (FieldErrorObserver).
type fieldErrObserver struct {
	errs map[core.BEField]error
}

func newFieldErrObserver() *fieldErrObserver {
	return &fieldErrObserver{errs: map[core.BEField]error{}}
}

func (o *fieldErrObserver) OnRetrieveStart(*core.RetrieveContext)  {}
func (o *fieldErrObserver) OnRetrieveEnd(*core.RetrieveContext)    {}
func (o *fieldErrObserver) OnMatch(core.DocID, core.ConjID)        {}
func (o *fieldErrObserver) OnExcludeSkip(core.DocID)               {}
func (o *fieldErrObserver) OnCursorInit(int)                       {}
func (o *fieldErrObserver) OnFieldError(f core.BEField, err error) { o.errs[f] = err }

// A "number" field queried with a value that cannot be tokenized to a number
// makes Encoder.Query fail. This exercises the strict/lenient boundary.
func badNumberQuery() core.Assignments {
	return core.Assignments{"age": true} // bool is unsupported by ValuesToStrings
}

// Local IndexOpt helpers mirroring the root package's WithStrictQuery/
// WithObserver, defined here to keep this test at the engine layer without a
// dependency on the root package.
func withStrictQuery() core.IndexOpt {
	return func(ctx *core.RetrieveContext) { ctx.StrictQuery = true }
}

func withObserver(obs core.RetrieveObserver) core.IndexOpt {
	return func(ctx *core.RetrieveContext) { ctx.Observer = obs }
}

func TestRetrieve_LenientSkipsQueryError(t *testing.T) {
	eng := buildStrictEngine(t)
	// Default (lenient): the errored field is skipped, retrieval returns no error.
	res, err := eng.Retrieve(badNumberQuery())
	if err != nil {
		t.Fatalf("lenient retrieve must not error, got: %v", err)
	}
	if res.Cardinality() != 0 {
		t.Fatalf("expected empty result, got cardinality %d", res.Cardinality())
	}
}

func TestRetrieve_StrictReturnsQueryError(t *testing.T) {
	eng := buildStrictEngine(t)
	_, err := eng.Retrieve(badNumberQuery(), withStrictQuery())
	if err == nil {
		t.Fatal("strict retrieve must return the query error")
	}
}

func TestRetrieve_LenientSurfacesErrorViaObserver(t *testing.T) {
	eng := buildStrictEngine(t)
	obs := newFieldErrObserver()
	collector := core.NewDocIDCollector()
	err := eng.RetrieveWithCollector(badNumberQuery(), collector, withObserver(obs))
	if err != nil {
		t.Fatalf("lenient retrieve must not error, got: %v", err)
	}
	if _, ok := obs.errs["age"]; !ok {
		t.Fatal("expected OnFieldError to report the skipped field error")
	}
}

// A valid query must be unaffected by either mode.
func TestRetrieve_ValidQueryUnaffectedByStrict(t *testing.T) {
	eng := buildStrictEngine(t)
	for _, strict := range []bool{false, true} {
		opts := []core.IndexOpt{}
		if strict {
			opts = append(opts, withStrictQuery())
		}
		res, err := eng.Retrieve(core.Assignments{"age": 18}, opts...)
		if err != nil {
			t.Fatalf("valid query (strict=%v) errored: %v", strict, err)
		}
		if res.Cardinality() != 1 {
			t.Fatalf("valid query (strict=%v) want 1 match, got %d", strict, res.Cardinality())
		}
	}
}
