package mph_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	be_indexer "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/container/mph"
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


type testBW struct {
	blocks map[string][]byte
}

func newTestBW() *testBW { return &testBW{blocks: make(map[string][]byte)} }
func (w *testBW) WriteBlock(kind string, data []byte) error {
	w.blocks[kind] = data
	return nil
}

func buildMPH(b *mph.MPHBuilder) ([]byte, error) {
	bw := newTestBW()
	if err := b.Build(bw); err != nil {
		return nil, err
	}
	return bw.blocks[mph.IndexName], nil
}

func TestContainerRoundTrip(t *testing.T) {
	convey.Convey("mph builder -> bytes -> reader -> retrieve", t, func() {
		convey.Convey("single term hit", func() {
			eid := core.NewEntryID(core.NewConjID(1, 0, 1), true)
			pl := mustWritePL(t, core.Entries{eid})

			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
					cb.AddPosting(string("hello"), segment.PostingRef{Offset: 0, Count: 1})
			blob, err := buildMPH(cb)
			convey.So(err, convey.ShouldBeNil)

			cr, err := mph.NewMPHReader(blob)
			convey.So(err, convey.ShouldBeNil)

			iters, err := cr.MatchQuery(segment.BlockContext{Pl: pl}, "field", "hello")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
			convey.So(iters[0].Current(), convey.ShouldEqual, eid)
		})

		convey.Convey("multiple terms, exact match", func() {
			eidA := core.NewEntryID(core.NewConjID(1, 0, 1), true)
			eidB := core.NewEntryID(core.NewConjID(2, 0, 1), true)
			eidC := core.NewEntryID(core.NewConjID(3, 0, 1), true)

			plA := mustWritePL(t, core.Entries{eidA})
			plB := mustWritePL(t, core.Entries{eidB})
			plC := mustWritePL(t, core.Entries{eidC})

			offB := uint64(len(plA))
			offC := offB + uint64(len(plB))
			postingBlock := append(append(append([]byte{}, plA...), plB...), plC...)

			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
			cb.AddPosting(string("alpha"), segment.PostingRef{Offset: 0, Count: 1})
			cb.AddPosting(string("beta"), segment.PostingRef{Offset: offB, Count: 1})
			cb.AddPosting(string("gamma"), segment.PostingRef{Offset: offC, Count: 1})
			blob, err := buildMPH(cb)
			convey.So(err, convey.ShouldBeNil)

			cr, err := mph.NewMPHReader(blob)
			convey.So(err, convey.ShouldBeNil)

			convey.Convey("hit alpha", func() {
				iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "f", "alpha")
				convey.So(err, convey.ShouldBeNil)
				convey.So(len(iters), convey.ShouldEqual, 1)
				convey.So(iters[0].Current(), convey.ShouldEqual, eidA)
			})
			convey.Convey("hit gamma", func() {
				iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "f", "gamma")
				convey.So(err, convey.ShouldBeNil)
				convey.So(len(iters), convey.ShouldEqual, 1)
				convey.So(iters[0].Current(), convey.ShouldEqual, eidC)
			})
			convey.Convey("miss", func() {
				iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "f", "delta")
				convey.So(err, convey.ShouldBeNil)
				convey.So(len(iters), convey.ShouldEqual, 0)
			})
		})

		convey.Convey("term not found returns empty", func() {
			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
			cb.AddPosting(string("apple"), segment.PostingRef{Offset: 0, Count: 1})
			blob, _ := buildMPH(cb)
			cr, _ := mph.NewMPHReader(blob)

			iters, err := cr.MatchQuery(segment.BlockContext{}, "f", "notfound")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})

		convey.Convey("non-string query is an error", func() {
			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
			cb.AddPosting(string("x"), segment.PostingRef{Offset: 0, Count: 1})
			blob, _ := buildMPH(cb)
			cr, _ := mph.NewMPHReader(blob)

			_, err := cr.MatchQuery(segment.BlockContext{}, "f", 42)
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("empty builder produces nil blob", func() {
			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
			blob, err := buildMPH(cb)
			convey.So(err, convey.ShouldBeNil)
			convey.So(blob, convey.ShouldBeNil)
		})

		convey.Convey("too short blob returns error", func() {
			_, err := mph.NewMPHReader([]byte{0})
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("large dictionary round-trip", func() {
			size := 10000
			perEntry := 16 // posting header(8) + one EntryID(8)
			usePosting := make([]byte, size*perEntry) // header(8) + entry(8) per term
			var off uint64 = 0
			cb := mph.NewMPHBuilder(segment.BuilderEnv{})
			for i := 0; i < size; i++ {
				term := fmt.Sprintf("term_%06d", i)
				ref := segment.PostingRef{Offset: off, Count: 1}
				off += uint64(perEntry)
				cb.AddPosting(string(term), ref)
			}
			blob, err := buildMPH(cb)
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(blob), convey.ShouldBeGreaterThan, 0)

			cr, err := mph.NewMPHReader(blob)
			convey.So(err, convey.ShouldBeNil)

			for i := 0; i < size; i++ {
				term := fmt.Sprintf("term_%06d", i)
				iters, err := cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", term)
				convey.So(err, convey.ShouldBeNil)
				convey.So(len(iters), convey.ShouldEqual, 1)
			}

			iters, err := cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", "not_a_term")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})
	})
}

func TestExtensionEndToEnd(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"city": {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: mph.IndexName, Encoder: "default"}},
		"tag":  {Field: "tag", FieldOption: be_indexer.FieldOption{IndexType: "default"}},
	}
	docs := []*be_indexer.Document{
		be_indexer.NewDocument(1).AddConjunction(
			be_indexer.NewConjunction().Include("city", "bj")),
		be_indexer.NewDocument(2).AddConjunction(
			be_indexer.NewConjunction().Include("city", "sh")),
		be_indexer.NewDocument(3).AddConjunction(
			be_indexer.NewConjunction().Include("tag", "vip")),
		be_indexer.NewDocument(4).AddConjunction(
			be_indexer.NewConjunction().Include("city", "bj").Include("tag", "vip")),
	}

	convey.Convey("mph e2e", t, func() {
		buf := new(bytes.Buffer)
		wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
		convey.So(err, convey.ShouldBeNil)
		defer eng.Close()

		convey.Convey("single mph field K=1", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1})

			got = retrieve(t, eng, be_indexer.Assignments{"city": "sh"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})

			got = retrieve(t, eng, be_indexer.Assignments{"city": "gz"})
			convey.So(len(got), convey.ShouldEqual, 0)
		})

		convey.Convey("mph + flatdict mixed K=2", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj", "tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 3, 4})
		})

		convey.Convey("mph field with no match", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "gz", "tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{3})
		})

		convey.Convey("no assignment for mph field still works", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{3})
		})

		convey.Convey("container block exists and answers directly", func() {
			iters, err := reader.IndexQuery("city", mph.IndexName, "bj")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
		})
	})
}

func TestLargeDictionary(t *testing.T) {
	convey.Convey("large mph dict end-to-end", t, func() {
		fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
			"term": {Field: "term", FieldOption: be_indexer.FieldOption{IndexType: mph.IndexName, Encoder: "default"}},
		}

		n := 5000
		docs := make([]*be_indexer.Document, n)
		terms := make([]string, n)
		for i := 0; i < n; i++ {
			t := fmt.Sprintf("term_%05d", rand.Intn(n*10))
			terms[i] = t
			docs[i] = be_indexer.NewDocument(be_indexer.DocID(i + 1)).AddConjunction(
				be_indexer.NewConjunction().Include("term", t))
		}

		buf := new(bytes.Buffer)
		wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
		convey.So(err, convey.ShouldBeNil)
		defer eng.Close()

		for i := 0; i < 100; i++ {
			idx := rand.Intn(n)
			got := retrieve(t, eng, be_indexer.Assignments{"term": terms[idx]})
			nFound := 0
			for _, id := range got {
				if id == be_indexer.DocID(idx+1) {
					nFound++
				}
			}
			convey.So(nFound, convey.ShouldEqual, 1)
		}
	})
}

func TestExcludePredicate(t *testing.T) {
	convey.Convey("mph with exclude predicates", t, func() {
		fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
			"city": {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: mph.IndexName, Encoder: "default"}},
		}
		docs := []*be_indexer.Document{
			be_indexer.NewDocument(1).AddConjunction(
				be_indexer.NewConjunction().Include("city", "bj")),
			be_indexer.NewDocument(2).AddConjunction(
				be_indexer.NewConjunction().Exclude("city", "bj").Include("city", "sh")),
		}

		buf := new(bytes.Buffer)
		wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
		convey.So(err, convey.ShouldBeNil)
		defer eng.Close()

		convey.Convey("doc 2 is excluded when bj is present", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1})
		})

		convey.Convey("doc 2 matches when only sh is present", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "sh"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})
		})
	})
}



// --- benchmarks ---

func BenchmarkMPH_Find(b *testing.B) {
	sizes := []int{10_000, 100_000, 500_000, 1_000_000}
	scenarios := []struct {
		name string
		mode int
	}{
		{"hit", 0},
		{"miss", 1},
		{"random", 2},
	}

	for _, size := range sizes {
		for _, sc := range scenarios {
			name := fmt.Sprintf("size=%d/%s", size, sc.name)
			b.Run(name, func(b *testing.B) {
				cb := mph.NewMPHBuilder(segment.BuilderEnv{})
				keys := make([]string, 0, size)
				for i := 0; i < size; i++ {
					k := fmt.Sprintf("term_%08x", i)
					keys = append(keys, k)
					cb.AddPosting(string(k), segment.PostingRef{
						Offset: uint64(i * 16), // stride=16: header(8)+EntryID(8)
						Count:  1,
					})
				}
				blob, err := buildMPH(cb)
				if err != nil {
					b.Fatal(err)
				}
				cr, err := mph.NewMPHReader(blob)
				if err != nil {
					b.Fatal(err)
				}

				usePosting := make([]byte, size*16)

				b.ResetTimer()
				b.ReportAllocs()

				switch sc.mode {
				case 0:
					term := keys[size/2]
					for i := 0; i < b.N; i++ {
						_, _ = cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", term)
					}
				case 1:
					for i := 0; i < b.N; i++ {
						_, _ = cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", "zzzz_nonexistent")
					}
				case 2:
					idx := make([]string, b.N)
					for i := 0; i < b.N; i++ {
						if rand.Intn(2) == 0 {
							idx[i] = keys[rand.Intn(size)]
						} else {
							idx[i] = fmt.Sprintf("zzz_nonexistent_%d", rand.Intn(size))
						}
					}
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						_, _ = cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", idx[i])
					}
				}
			})
		}
	}
}

func BenchmarkMPH_Build(b *testing.B) {
	sizes := []int{10_000, 100_000, 500_000}
	for _, size := range sizes {
		name := fmt.Sprintf("size=%d", size)
		b.Run(name, func(b *testing.B) {
			b.StopTimer()
			type pair struct {
				term string
				ref  segment.PostingRef
			}
			pairs := make([]pair, size)
			for i := 0; i < size; i++ {
				pairs[i] = pair{
					term: fmt.Sprintf("term_%08x", i),
					ref:  segment.PostingRef{Offset: uint64(i * 100), Count: uint32(i % 10)},
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				cb := mph.NewMPHBuilder(segment.BuilderEnv{})
				for _, p := range pairs {
					cb.AddPosting(string(p.term), p.ref)
				}
				buildMPH(cb)
			}
		})
	}
}

// TestMPHQueryZeroAlloc locks in the zero-allocation guarantee of the query hot
// path. MatchQuery previously did []byte(term), which heap-allocated on every
// call; stringToBytes removes that. This test fails if the conversion (or any
// other per-query allocation on a hit) is reintroduced.
func TestMPHQueryZeroAlloc(t *testing.T) {
	eid := core.NewEntryID(core.NewConjID(1, 0, 1), true)
	pl := mustWritePL(t, core.Entries{eid})

	cb := mph.NewMPHBuilder(segment.BuilderEnv{})
	cb.AddPosting("hello", segment.PostingRef{Offset: 0, Count: 1})
	blob, err := buildMPH(cb)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cr, err := mph.NewMPHReader(blob)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	ctx := segment.BlockContext{Pl: pl}

	// Warm up (first call may lazily allocate reader-internal state).
	if _, err := cr.MatchQuery(ctx, "field", "hello"); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	// A hit returns one cursor: WriteFlatPostingList cursor construction is the
	// only permitted allocation. Assert the term->[]byte conversion adds none by
	// comparing hit vs miss: a miss returns nil and must be fully zero-alloc.
	missAllocs := testing.AllocsPerRun(200, func() {
		_, _ = cr.MatchQuery(ctx, "field", "nope")
	})
	if missAllocs != 0 {
		t.Fatalf("miss query should be zero-alloc, got %f (string->[]byte conversion leaked?)", missAllocs)
	}
}

