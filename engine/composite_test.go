package engine_test

import (
	"bytes"
	"errors"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

func buildCompositeTestEngine(t *testing.T, docs []*core.Document) *engine.BooleanEngine {
	t.Helper()
	fields := core.Schema{
		"a": {Encoder: "number"},
		"b": {Encoder: "number"},
	}
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

func sortedIDs(ids core.DocIDList) core.DocIDList {
	out := append(core.DocIDList(nil), ids...)
	sort.Sort(out)
	return out
}

func assertIDs(t *testing.T, got core.DocIDList, want ...core.DocID) {
	t.Helper()
	got = sortedIDs(got)
	if len(got) != len(want) {
		t.Fatalf("ids length mismatch: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids mismatch: got=%v want=%v", got, want)
		}
	}
}

type compositeCollector struct {
	ids []core.DocID
}

func (c *compositeCollector) Add(id core.DocID) { c.ids = append(c.ids, id) }

func collectComposite(t *testing.T, ce *engine.CompositeEngine, query core.Assignments) core.DocIDList {
	t.Helper()
	collector := &compositeCollector{}
	if err := ce.RetrieveWithCollector(query, collector); err != nil {
		t.Fatal(err)
	}
	return collector.ids
}

func TestCompositeEngine_UpdateStillMatchesUsesDelta(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		Generation:   2,
		FullEngine:   full,
		DeltaEngines: []*engine.BooleanEngine{delta},
		ChangedDocs:  core.NewBitmapDocSet(1),
	})

	b, err := ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids, 1)

	b, err = ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	assertIDs(t, ids)
}

func TestCompositeEngine_UpdateNoLongerMatchesRemovesFull(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:   full,
		DeltaEngines: []*engine.BooleanEngine{delta},
		ChangedDocs:  core.NewBitmapDocSet(2),
	})

	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids)
}

func TestCompositeEngine_DeleteRemovesFull(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:  full,
		ChangedDocs: core.NewBitmapDocSet(3),
		DeletedDocs: core.NewBitmapDocSet(3),
	})

	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids)
}

func TestCompositeEngine_DeleteThenRecreateReturnsDelta(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:   full,
		DeltaEngines: []*engine.BooleanEngine{delta},
		ChangedDocs:  core.NewBitmapDocSet(4),
		// DeletedDocs is empty because the latest mutation is recreate/upsert.
		DeletedDocs: core.NewBitmapDocSet(),
	})

	b, err := ce.Retrieve(core.Assignments{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids, 4)
}

func TestCompositeEngine_DeduplicatesFinalResult(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(5).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:   full,
		DeltaEngines: []*engine.BooleanEngine{delta},
		ChangedDocs:  core.NewBitmapDocSet(5),
	})

	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids, 5)
}

func TestCompositeEngine_ExcludeMissingSemantics(t *testing.T) {
	idx := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(6).AddConjunction(core.NewConjunction().In("a", 1).NotIn("b", 2)),
		core.NewDocument(7).AddConjunction(core.NewConjunction().NotIn("b", 2)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{FullEngine: idx})

	b, err := ce.Retrieve(core.Assignments{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	ids := bitmapToSlice(b)
	assertIDs(t, ids, 6, 7)

	b, err = ce.Retrieve(core.Assignments{"a": 1, "b": 3})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	assertIDs(t, ids, 6, 7)

	b, err = ce.Retrieve(core.Assignments{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	assertIDs(t, ids)

	b, err = ce.Retrieve(core.Assignments{"a": 1, "b": []int{}})
	if err != nil {
		t.Fatal(err)
	}
	ids = bitmapToSlice(b)
	assertIDs(t, ids, 6, 7)
}

func TestCompositeEngine_RetrieveWithCollectorFinalDocIDs(t *testing.T) {
	full := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta := buildCompositeTestEngine(t, []*core.Document{
		// doc 1 is updated but still matches; doc 3 is recreated and matches.
		core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1)),
		core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 1)),
		// doc 4 is updated and no longer matches a=1.
		core.NewDocument(4).AddConjunction(core.NewConjunction().In("a", 2)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		FullEngine:   full,
		DeltaEngines: []*engine.BooleanEngine{delta},
		ChangedDocs:  core.NewBitmapDocSet(1, 2, 3, 4),
		// doc 2 is deleted; doc 3's latest state is recreate/upsert.
		DeletedDocs: core.NewBitmapDocSet(2),
	})

	assertIDs(t, collectComposite(t, ce, core.Assignments{"a": 1}), 1, 3)
}

func TestCompositeEngine_RetrieveWithCollectorDeduplicatesAcrossDeltas(t *testing.T) {
	delta1 := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(8).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	delta2 := buildCompositeTestEngine(t, []*core.Document{
		core.NewDocument(8).AddConjunction(core.NewConjunction().In("a", 1)),
	})
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{
		DeltaEngines: []*engine.BooleanEngine{delta1, delta2},
		ChangedDocs:  core.NewBitmapDocSet(8),
	})

	assertIDs(t, collectComposite(t, ce, core.Assignments{"a": 1}), 8)
}

func TestCompositeEngine_RetrieveWithCollectorRejectsNil(t *testing.T) {
	ce := engine.NewCompositeEngine(&engine.IndexSnapshot{})
	if err := ce.RetrieveWithCollector(nil, nil); !errors.Is(err, core.ErrNilResultCollector) {
		t.Fatalf("expected ErrNilResultCollector, got %v", err)
	}
}
