package fst_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	be_indexer "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/container/fst"
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

// testBW captures blocks written by a builder for round-trip tests.
type testBW struct {
	blocks map[string][]byte
}

func newTestBW() *testBW { return &testBW{blocks: make(map[string][]byte)} }
func (w *testBW) WriteBlock(kind string, data []byte) error {
	w.blocks[kind] = data
	return nil
}

// buildFST runs the builder and returns the FST block bytes.
func buildFST(b *fst.FSTBuilder) ([]byte, error) {
	bw := newTestBW()
	if err := b.Build(bw); err != nil {
		return nil, err
	}
	return bw.blocks[fst.IndexName], nil
}

func TestContainerRoundTrip(t *testing.T) {
	convey.Convey("fst builder -> bytes -> reader -> retrieve", t, func() {
		convey.Convey("single term hit", func() {
			eid := core.NewEntryID(core.NewConjID(1, 0, 1), true)
			pl := mustWritePL(t, core.Entries{eid})

			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			cb.AddPosting("hello", segment.PostingRef{Offset: 0, Count: 1})
			blob, err := buildFST(cb)
			convey.So(err, convey.ShouldBeNil)

			cr, err := fst.NewFSTReader(blob)
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

			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			cb.AddPosting("alpha", segment.PostingRef{Offset: 0, Count: 1})
			cb.AddPosting("beta", segment.PostingRef{Offset: offB, Count: 1})
			cb.AddPosting("gamma", segment.PostingRef{Offset: offC, Count: 1})
			blob, err := buildFST(cb)
			convey.So(err, convey.ShouldBeNil)

			cr, err := fst.NewFSTReader(blob)
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

		convey.Convey("prefix-sharing terms round-trip", func() {
			// Terms with heavy shared prefixes exercise the FST's affix folding.
			terms := []string{
				"com.example.app.alpha",
				"com.example.app.beta",
				"com.example.app.gamma",
				"com.example.svc.delta",
				"com.example.svc.epsilon",
			}
			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			for i, term := range terms {
				cb.AddPosting(term, segment.PostingRef{Offset: uint64(i * 16), Count: 1})
			}
			blob, err := buildFST(cb)
			convey.So(err, convey.ShouldBeNil)

			cr, err := fst.NewFSTReader(blob)
			convey.So(err, convey.ShouldBeNil)

			usePosting := make([]byte, len(terms)*16)
			for _, term := range terms {
				iters, err := cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", term)
				convey.So(err, convey.ShouldBeNil)
				convey.So(len(iters), convey.ShouldEqual, 1)
			}
			iters, err := cr.MatchQuery(segment.BlockContext{Pl: usePosting}, "f", "com.example.app")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0) // exact match only, not prefix
		})

		convey.Convey("term not found returns empty", func() {
			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			cb.AddPosting("apple", segment.PostingRef{Offset: 0, Count: 1})
			blob, _ := buildFST(cb)
			cr, _ := fst.NewFSTReader(blob)

			iters, err := cr.MatchQuery(segment.BlockContext{}, "f", "notfound")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})

		convey.Convey("non-string query is an error", func() {
			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			cb.AddPosting("x", segment.PostingRef{Offset: 0, Count: 1})
			blob, _ := buildFST(cb)
			cr, _ := fst.NewFSTReader(blob)

			_, err := cr.MatchQuery(segment.BlockContext{}, "f", 42)
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("empty builder produces nil blob", func() {
			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			blob, err := buildFST(cb)
			convey.So(err, convey.ShouldBeNil)
			convey.So(blob, convey.ShouldBeNil)
		})

		convey.Convey("empty blob reader answers empty", func() {
			cr, err := fst.NewFSTReader(nil)
			convey.So(err, convey.ShouldBeNil)
			iters, err := cr.MatchQuery(segment.BlockContext{}, "f", "anything")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})

		convey.Convey("corrupt blob returns error", func() {
			_, err := fst.NewFSTReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("large dictionary round-trip", func() {
			size := 10000
			perEntry := 16 // header(8) + one EntryID(8)
			usePosting := make([]byte, size*perEntry)
			cb := fst.NewFSTBuilder(segment.BuilderEnv{})
			for i := 0; i < size; i++ {
				term := fmt.Sprintf("term_%06d", i)
				cb.AddPosting(term, segment.PostingRef{Offset: uint64(i * perEntry), Count: 1})
			}
			blob, err := buildFST(cb)
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(blob), convey.ShouldBeGreaterThan, 0)

			cr, err := fst.NewFSTReader(blob)
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
		"city": {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: fst.IndexName, Encoder: "default"}},
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

	convey.Convey("fst e2e", t, func() {
		buf := new(bytes.Buffer)
		err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
		convey.So(err, convey.ShouldBeNil)
		defer eng.Close()

		convey.Convey("single fst field K=1", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1})

			got = retrieve(t, eng, be_indexer.Assignments{"city": "sh"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})

			got = retrieve(t, eng, be_indexer.Assignments{"city": "gz"})
			convey.So(len(got), convey.ShouldEqual, 0)
		})

		convey.Convey("fst + flatdict mixed K=2", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj", "tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 3, 4})
		})

		convey.Convey("fst field with no match", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "gz", "tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{3})
		})

		convey.Convey("no assignment for fst field still works", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"tag": "vip"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{3})
		})

		convey.Convey("container block exists and answers directly", func() {
			iters, err := reader.IndexQuery("city", fst.IndexName, "bj")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
		})
	})
}

func TestLargeDictionary(t *testing.T) {
	convey.Convey("large fst dict end-to-end", t, func() {
		fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
			"term": {Field: "term", FieldOption: be_indexer.FieldOption{IndexType: fst.IndexName, Encoder: "default"}},
		}

		n := 5000
		docs := make([]*be_indexer.Document, n)
		terms := make([]string, n)
		for i := 0; i < n; i++ {
			tk := fmt.Sprintf("term_%05d", rand.Intn(n*10))
			terms[i] = tk
			docs[i] = be_indexer.NewDocument(be_indexer.DocID(i + 1)).AddConjunction(
				be_indexer.NewConjunction().Include("term", tk))
		}

		buf := new(bytes.Buffer)
		err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
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
	convey.Convey("fst with exclude predicates", t, func() {
		fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
			"city": {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: fst.IndexName, Encoder: "default"}},
		}
		docs := []*be_indexer.Document{
			be_indexer.NewDocument(1).AddConjunction(
				be_indexer.NewConjunction().Include("city", "bj")),
			be_indexer.NewDocument(2).AddConjunction(
				be_indexer.NewConjunction().Exclude("city", "bj").Include("city", "sh")),
		}

		buf := new(bytes.Buffer)
		err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})
		convey.So(err, convey.ShouldBeNil)

		reader, err := be_indexer.NewSegmentReader(buf.Bytes())
		convey.So(err, convey.ShouldBeNil)

		eng, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
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

// TestShadowAgainstDefault verifies the fst container returns identical results
// to the default FlatDict container across random queries, proving fst is a
// drop-in dictionary replacement.
func TestShadowAgainstDefault(t *testing.T) {
	convey.Convey("fst matches default container on random queries", t, func() {
		n := 2000
		vocab := make([]string, 0, 500)
		for i := 0; i < 500; i++ {
			vocab = append(vocab, fmt.Sprintf("v_%04d", i))
		}
		docs := make([]*be_indexer.Document, n)
		for i := 0; i < n; i++ {
			v := vocab[rand.Intn(len(vocab))]
			docs[i] = be_indexer.NewDocument(be_indexer.DocID(i + 1)).AddConjunction(
				be_indexer.NewConjunction().Include("k", v))
		}

		build := func(indexType string) *be_indexer.Engine {
			fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
				"k": {Field: "k", FieldOption: be_indexer.FieldOption{IndexType: indexType, Encoder: "default"}},
			}
			buf := new(bytes.Buffer)
			err := be_indexer.BuildSegment(buf, fields, docs, be_indexer.BuildSegmentOptions{})
			convey.So(err, convey.ShouldBeNil)
			reader, err := be_indexer.NewSegmentReader(buf.Bytes())
			convey.So(err, convey.ShouldBeNil)
			eng, err := be_indexer.NewEngine(fields, []*be_indexer.SegmentReader{reader})
			convey.So(err, convey.ShouldBeNil)
			return eng
		}

		fstEng := build(fst.IndexName)
		defer fstEng.Close()
		defEng := build("default")
		defer defEng.Close()

		for i := 0; i < 300; i++ {
			var v string
			if rand.Intn(3) == 0 {
				v = fmt.Sprintf("missing_%d", rand.Intn(1000)) // sometimes a miss
			} else {
				v = vocab[rand.Intn(len(vocab))]
			}
			gotFST := retrieve(t, fstEng, be_indexer.Assignments{"k": v})
			gotDef := retrieve(t, defEng, be_indexer.Assignments{"k": v})
			convey.So(gotFST, convey.ShouldResemble, gotDef)
		}
	})
}

// TestQueryMissZeroAlloc locks in the allocation-free miss path. The pooled
// *vellum.Reader reuses its internal transducer state across calls, so a lookup
// that walks the FST and finds no match must not allocate. This fails if the
// Reader pool regresses to plain FST.Get (which allocates a state struct per
// call) or if stringToBytes reintroduces a []byte(term) copy.
func TestQueryMissZeroAlloc(t *testing.T) {
	cb := fst.NewFSTBuilder(segment.BuilderEnv{})
	cb.AddPosting("hello", segment.PostingRef{Offset: 0, Count: 1})
	blob, err := buildFST(cb)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cr, err := fst.NewFSTReader(blob)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	ctx := segment.BlockContext{}

	// Warm the pool so the first New() allocation is not counted.
	if _, err := cr.MatchQuery(ctx, "field", "nope"); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	missAllocs := testing.AllocsPerRun(200, func() {
		_, _ = cr.MatchQuery(ctx, "field", "nope")
	})
	if missAllocs != 0 {
		t.Fatalf("miss query should be zero-alloc, got %f (Reader pool or stringToBytes regressed?)", missAllocs)
	}
}

// TestConcurrentMatchQuery exercises the IndexReader concurrency contract. A
// single *vellum.Reader is documented as single-threaded, so FSTIndex pools one
// per in-flight query; this test would data-race (under -race) or corrupt
// results if a shared Reader leaked across goroutines.
func TestConcurrentMatchQuery(t *testing.T) {
	size := 2000
	cb := fst.NewFSTBuilder(segment.BuilderEnv{})
	pl := make([]byte, 0, size*16)
	for i := 0; i < size; i++ {
		eid := core.NewEntryID(core.NewConjID(core.DocID(i+1), 0, 1), true)
		cb.AddPosting(fmt.Sprintf("term_%06d", i), segment.PostingRef{Offset: uint64(len(pl)), Count: 1})
		pl = append(pl, mustWritePL(t, core.Entries{eid})...)
	}
	blob, err := buildFST(cb)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cr, err := fst.NewFSTReader(blob)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	ctx := segment.BlockContext{Pl: pl}

	const goroutines = 16
	done := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			r := rand.New(rand.NewSource(int64(seed)))
			for i := 0; i < 2000; i++ {
				idx := r.Intn(size)
				iters, err := cr.MatchQuery(ctx, "f", fmt.Sprintf("term_%06d", idx))
				if err != nil {
					done <- err
					return
				}
				if len(iters) != 1 {
					done <- fmt.Errorf("term_%06d: expected 1 iter, got %d", idx, len(iters))
					return
				}
			}
			done <- nil
		}(g)
	}
	for g := 0; g < goroutines; g++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// --- benchmarks ---

func BenchmarkFST_Find(b *testing.B) {
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
				cb := fst.NewFSTBuilder(segment.BuilderEnv{})
				keys := make([]string, 0, size)
				for i := 0; i < size; i++ {
					k := fmt.Sprintf("term_%08x", i)
					keys = append(keys, k)
					cb.AddPosting(k, segment.PostingRef{Offset: uint64(i * 16), Count: 1})
				}
				blob, err := buildFST(cb)
				if err != nil {
					b.Fatal(err)
				}
				cr, err := fst.NewFSTReader(blob)
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

func BenchmarkFST_Build(b *testing.B) {
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
				cb := fst.NewFSTBuilder(segment.BuilderEnv{})
				for _, p := range pairs {
					cb.AddPosting(p.term, p.ref)
				}
				_, _ = buildFST(cb)
			}
		})
	}
}
