package example_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	be_indexer "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/container/example"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

// mustWritePL serializes a posting list for tests, failing on the (unreachable
// for test-sized inputs) overflow error.
func mustWritePL(t *testing.T, entries core.Entries) []byte {
	t.Helper()
	b, err := segment.WriteFlatPostingList(entries)
	if err != nil {
		t.Fatal(err)
	}
	return b
}


type testBW struct {
	blocks map[string][]byte
}

func newTestBW() *testBW { return &testBW{blocks: make(map[string][]byte)} }
func (w *testBW) WriteBlock(kind string, data []byte) error {
	w.blocks[kind] = data
	return nil
}

func buildPrefix(b *example.PrefixBuilder) ([]byte, error) {
	bw := newTestBW()
	if err := b.Build(bw); err != nil {
		return nil, err
	}
	return bw.blocks[example.IndexName], nil
}

func TestContainerRoundTrip(t *testing.T) {
	convey.Convey("builder → bytes → reader → retrieve", t, func() {
		eidA := core.NewEntryID(core.NewConjID(101, 0, 1), true)
		eidB := core.NewEntryID(core.NewConjID(102, 0, 1), true)

		plA := mustWritePL(t, core.Entries{eidA})
		plB := mustWritePL(t, core.Entries{eidB})
		postingBlock := append(append([]byte{}, plA...), plB...)

	cb := example.NewPrefixBuilder(segment.BuilderEnv{})
	cb.AddPosting("/api", segment.PostingRef{Offset: 0, Count: 1})
	cb.AddPosting("/static", segment.PostingRef{Offset: uint64(len(plA)), Count: 1})
	blob, err := buildPrefix(cb)
		convey.So(err, convey.ShouldBeNil)

		cr, err := example.NewPrefixReader(blob)
		convey.So(err, convey.ShouldBeNil)

		convey.Convey("prefix hit returns the matching posting", func() {
			iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "path", "/api/v1/users")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
			convey.So(iters[0].Current(), convey.ShouldEqual, eidA)
		})

		convey.Convey("no prefix match returns nothing", func() {
			iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "path", "/other")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})

		convey.Convey("non-string query is an error", func() {
			_, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "path", 42)
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("truncated blob is an error", func() {
			_, err := example.NewPrefixReader(blob[:len(blob)-3])
			convey.So(err, convey.ShouldNotBeNil)
			_, err = example.NewPrefixReader([]byte{1})
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func retrieve(t *testing.T, eng *be_indexer.Engine, assigns be_indexer.Assignments) []be_indexer.DocID {
	t.Helper()
	res, err := eng.Retrieve(assigns)
	if err != nil {
		t.Fatalf("retrieve %+v: %v", assigns, err)
	}
	var ids be_indexer.DocIDList
	res.ForEach(func(id be_indexer.DocID) { ids = append(ids, id) })
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestExtensionEndToEnd guards the full custom-container chain for library
// users: RegisterPredicateEncoder + RegisterIndex → doc_exporter default
// branch → segment container block → SegmentReader load → engine
// default branch → IndexQuery → MatchQuery.
func TestExtensionEndToEnd(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"path": {Field: "path", FieldOption: be_indexer.FieldOption{IndexType: example.IndexName}},
		"city": {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: "default"}},
	}
	docs := []*be_indexer.Document{
		be_indexer.NewDocument(1).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/api")),
		be_indexer.NewDocument(2).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/static")),
		be_indexer.NewDocument(3).AddConjunction(
			be_indexer.NewConjunction().Include("city", "bj")),
		be_indexer.NewDocument(4).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/api").Include("city", "bj")),
	}

	buf := new(bytes.Buffer)
	wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := be_indexer.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	convey.Convey("extension e2e", t, func() {
		convey.Convey("container block exists and answers directly", func() {
			iters, err := reader.IndexQuery("path", example.IndexName, "/api/v1")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
		})

		convey.Convey("engine default branch routes custom kind", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"path": "/api/v1/users"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1})

			got = retrieve(t, eng, be_indexer.Assignments{"path": "/staticfiles/x.css"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})

			got = retrieve(t, eng, be_indexer.Assignments{"path": "/nomatch"})
			convey.So(len(got), convey.ShouldEqual, 0)
		})

		convey.Convey("custom container composes with normal fields (K=2)", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"path": "/api/x", "city": "bj"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 3, 4})
		})
	})
}
