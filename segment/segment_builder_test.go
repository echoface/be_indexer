package segment

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
)

func mkE(k int, id core.DocID) core.EntryID {
	return core.NewEntryID(core.NewConjID(id, 0, k), true)
}

// mustSegmentBuilder builds a SegmentBuilder for tests, failing the test on an
// invalid option combination.
func mustSegmentBuilder(t *testing.T, w io.Writer, opts SegmentBuilderOptions) *SegmentBuilder {
	t.Helper()
	b, err := NewSegmentBuilder(w, opts)
	if err != nil {
		t.Fatalf("NewSegmentBuilder: %v", err)
	}
	return b
}

// TestSegmentBuilderSpillMatchesInMemory proves the spill (external-sort) and
// pure in-memory profiles of the same builder emit byte-identical segments.
func TestSegmentBuilderSpillMatchesInMemory(t *testing.T) {
	fields := core.Schema{"a": {Encoder: "number"}}
	build := func(spill bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			AddField(core.BEField, core.FieldOption) error
			AddRecord(string, any, []core.EntryID) error
			Write() error
		}
		if spill {
			sink = mustSegmentBuilder(t, buf, SegmentBuilderOptions{
				MaxPostingsInMemory: 2,
				TmpDir:              t.TempDir(),
			})
		} else {
			sink = NewInMemorySegmentBuilder(buf)
		}
		sink.SetDocCount(3)
		for field, option := range fields {
			sink.AddField(field, option)
		}
		postings := []struct {
			term  string
			entry core.EntryID
		}{
			{term: "2", entry: mkE(1, 4)},
			{term: "1", entry: mkE(1, 2)},
			{term: "1", entry: mkE(1, 1)},
			{term: "2", entry: mkE(1, 5)},
			{term: "3", entry: mkE(1, 6)},
		}
		for _, posting := range postings {
			if err := sink.AddRecord("a", posting.term, []core.EntryID{posting.entry}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Write(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	want := build(false)
	got := build(true)
	if !bytes.Equal(got, want) {
		t.Fatalf("spill builder bytes mismatch")
	}

	reader, err := NewSegmentReader(got)
	if err != nil {
		t.Fatal(err)
	}
	it, err := reader.GetPostingsByTerm("a", "1")
	if err != nil {
		t.Fatal(err)
	}
	if it == nil {
		t.Fatal("expected posting iterator for term 1")
	}
	expected1 := mkE(1, 1)
	expected2 := mkE(1, 2)
	if it.Current() != expected1 || it.SkipTo(expected2) != expected2 {
		t.Fatalf("posting list order mismatch: got %d skipTo(%d)=%d want %d,%d",
			it.Current(), expected2, it.SkipTo(expected2), expected1, expected2)
	}
}

func TestSegmentBuilderACMatcherAcrossRuns(t *testing.T) {
	buf := new(bytes.Buffer)
	b := mustSegmentBuilder(t, buf, SegmentBuilderOptions{MaxPostingsInMemory: 1, TmpDir: t.TempDir()})
	b.SetDocCount(4)
	b.AddField("keyword", core.FieldOption{IndexType: core.IndexNameACMatcher, Encoder: core.IndexNameACMatcher})
	postings := []struct {
		term  string
		entry core.EntryID
	}{
		{term: "apple", entry: mkE(1, 2)},
		{term: "app", entry: mkE(1, 1)},
		{term: "banana", entry: mkE(1, 3)},
		{term: "tree", entry: mkE(1, 4)},
	}
	for _, posting := range postings {
		if err := b.AddRecord("keyword", posting.term, []core.EntryID{posting.entry}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Write(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	iters, err := reader.MultiPatternSearch("keyword", "I love apple and banana")
	if err != nil {
		t.Fatal(err)
	}
	// AC now returns posting refs (not terms), so assert on the matched EntryIDs:
	// "apple"->2, "app"->1, "banana"->3 all occur in the text.
	got := make([]core.EntryID, 0, len(iters))
	for _, iter := range iters {
		for e := iter.Current(); !e.IsNULLEntry(); e = iter.SkipTo(e + 1) {
			got = append(got, e)
		}
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	want := []core.EntryID{mkE(1, 1), mkE(1, 2), mkE(1, 3)}
	if len(got) != len(want) {
		t.Fatalf("entries length mismatch: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries mismatch: got=%v want=%v", got, want)
		}
	}
}

func TestSegmentBuilderV5EmbedsWildcards(t *testing.T) {
	buf := new(bytes.Buffer)
	b := mustSegmentBuilder(t, buf, SegmentBuilderOptions{
		MaxPostingsInMemory: 1,
		TmpDir:              t.TempDir(),
		Wildcards:           core.Entries{30, 10},
	})
	b.SetDocCount(2)
	b.AddField("a", core.FieldOption{Encoder: "number"})
	if err := b.AddRecord("a", "1", []core.EntryID{mkE(1, 20)}); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	expectedHash, err := core.ComputeSchemaHash(core.Schema{
		"a": {Encoder: "number"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reader.Version() != SegmentVersionV5 || reader.SchemaHash() != expectedHash {
		t.Fatalf("v5 metadata mismatch: version=%d schema=%s", reader.Version(), reader.SchemaHash())
	}
	wildcards := reader.Wildcards()
	if len(wildcards) != 2 || wildcards[0] != 10 || wildcards[1] != 30 {
		t.Fatalf("wildcards mismatch: %v", wildcards)
	}
}

// TestSegmentBuilderSpillMatchesInMemoryWildcards uses two fields so the
// in-memory vs spill parity covers the schema-hash/field ordering path too.
func TestSegmentBuilderSpillMatchesInMemoryWildcards(t *testing.T) {
	build := func(spill bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			SetWildcards(core.Entries)
			AddField(core.BEField, core.FieldOption) error
			AddRecord(string, any, []core.EntryID) error
			Write() error
		}
		if spill {
			sink = mustSegmentBuilder(t, buf, SegmentBuilderOptions{MaxPostingsInMemory: 1, TmpDir: t.TempDir()})
		} else {
			sink = NewInMemorySegmentBuilder(buf)
		}
		sink.SetDocCount(2)
		sink.SetWildcards(core.Entries{30, 10})
		sink.AddField("b", core.FieldOption{Encoder: "number"})
		sink.AddField("a", core.FieldOption{Encoder: "number"})
		for _, posting := range []struct {
			term  string
			entry core.EntryID
		}{
			{term: "2", entry: mkE(1, 30)},
			{term: "1", entry: mkE(1, 10)},
			{term: "1", entry: mkE(1, 20)},
		} {
			if err := sink.AddRecord("a", posting.term, []core.EntryID{posting.entry}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.AddRecord("b", "9", []core.EntryID{mkE(2, 40)}); err != nil {
			t.Fatal(err)
		}
		if err := sink.Write(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	if got, want := build(true), build(false); !bytes.Equal(got, want) {
		t.Fatal("spill v5 builder bytes mismatch")
	}
}

// TestSegmentBuilderWildcardStreamingMatchesBuffered proves the streamed
// wildcard-block-file path (SetWildcardsBlockFile → copyFromFile, no
// io.ReadAll) produces a byte-identical segment to the in-memory SetWildcards
// path. This guards the §4.2.2 streaming change against framing/checksum drift.
func TestSegmentBuilderWildcardStreamingMatchesBuffered(t *testing.T) {
	wildcards := core.Entries{10, 30, 50, 70}
	sorted := append(core.Entries(nil), wildcards...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	build := func(useBlockFile bool) []byte {
		buf := new(bytes.Buffer)
		tmp := t.TempDir()
		b := mustSegmentBuilder(t, buf, SegmentBuilderOptions{MaxPostingsInMemory: 1, TmpDir: tmp})
		b.SetDocCount(2)
		if err := b.AddField("a", core.FieldOption{Encoder: "number"}); err != nil {
			t.Fatal(err)
		}
		if err := b.AddRecord("a", "1", []core.EntryID{mkE(1, 20)}); err != nil {
			t.Fatal(err)
		}
		if useBlockFile {
			// The sidecar on-disk format equals encodeEntriesBlock (sorted).
			sidecar := filepath.Join(tmp, "wildcards.bin")
			if err := os.WriteFile(sidecar, encodeEntriesBlock(sorted), 0o644); err != nil {
				t.Fatal(err)
			}
			b.SetWildcardsBlockFile(sidecar)
		} else {
			b.SetWildcards(wildcards)
		}
		if err := b.Write(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	streamed := build(true)
	buffered := build(false)
	if !bytes.Equal(streamed, buffered) {
		t.Fatalf("streamed wildcard segment differs from buffered: %d vs %d bytes", len(streamed), len(buffered))
	}

	// And the streamed segment must decode the correct wildcard set.
	reader, err := NewSegmentReader(streamed)
	if err != nil {
		t.Fatal(err)
	}
	got := reader.Wildcards()
	if len(got) != len(sorted) {
		t.Fatalf("wildcard count mismatch: got %d want %d", len(got), len(sorted))
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("wildcard[%d] = %d, want %d", i, got[i], sorted[i])
		}
	}
}

// TestSegmentBuilderSpillsAndCleansRuns proves the spill plumbing is actually
// engaged (sorted runs hit disk while building) and that Write removes the spill
// directory afterwards. Without this, a regression to the pure in-memory profile
// would be invisible because both profiles emit identical bytes.
func TestSegmentBuilderSpillsAndCleansRuns(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	buf := new(bytes.Buffer)
	b := mustSegmentBuilder(t, buf, SegmentBuilderOptions{
		MaxPostingsInMemory: 1,
		TmpDir:              runsDir,
	})
	b.SetDocCount(2)
	if err := b.AddField("a", core.FieldOption{Encoder: "number"}); err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"1", "2", "3"} {
		if err := b.AddRecord("a", term, []core.EntryID{mkE(1, core.DocID(len(term)))}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("spill runs dir missing after exceeding threshold: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least one spilled run file")
	}

	if err := b.Write(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runsDir); !os.IsNotExist(err) {
		t.Fatalf("expected spill dir to be removed after Write, stat err=%v", err)
	}

	// The segment must still be valid and complete.
	if _, err := NewSegmentReader(buf.Bytes()); err != nil {
		t.Fatalf("spilled segment rejected by reader: %v", err)
	}
}

// TestNewSegmentBuilderRejectsSpillWithoutTmpDir pins the fail-fast contract:
// enabling spill without a temp dir is a configuration error, not a silent
// fallback to unbounded in-memory accumulation.
func TestNewSegmentBuilderRejectsSpillWithoutTmpDir(t *testing.T) {
	if _, err := NewSegmentBuilder(new(bytes.Buffer), SegmentBuilderOptions{MaxPostingsInMemory: 1}); err == nil {
		t.Fatal("expected error for spill enabled without TmpDir")
	}
	if _, err := NewSegmentBuilder(new(bytes.Buffer), SegmentBuilderOptions{MaxPostingsInMemory: 0}); err != nil {
		t.Fatalf("zero-value options must not fail: %v", err)
	}
}
