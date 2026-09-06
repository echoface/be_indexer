package segment

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
)

func mkE(k int, id core.DocID) core.EntryID {
	return core.NewEntryID(core.NewConjID(id, 0, k), true)
}

func TestExternalBuilderMatchesBuilderAcrossRuns(t *testing.T) {
	fields := []core.FieldMeta{{ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}}}
	build := func(useExternal bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			AddField(core.FieldMeta) error
			AddRecord(string, any, []core.EntryID) error
			Write() error
		}
		if useExternal {
			sink = NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{MaxPostingsInMemory: 2})
		} else {
			sink = NewInMemorySegmentBuilder(buf)
		}
		sink.SetDocCount(3)
		for _, field := range fields {
			sink.AddField(field)
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
		t.Fatalf("external builder bytes mismatch")
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

func TestExternalBuilderACMatcherAcrossRuns(t *testing.T) {
	buf := new(bytes.Buffer)
	b := NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{MaxPostingsInMemory: 1})
	b.SetDocCount(4)
	b.AddField(core.FieldMeta{
		Field:       core.BEField("keyword"),
		FieldOption: core.FieldOption{IndexType: core.IndexNameACMatcher, Encoder: core.IndexNameACMatcher},
	})
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

func TestExternalBuilderV4EmbedsWildcards(t *testing.T) {
	buf := new(bytes.Buffer)
	b := NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{
		MaxPostingsInMemory: 1,
		SchemaHash:          "sha256:schema",
		Wildcards:           core.Entries{30, 10},
	})
	b.SetDocCount(2)
	b.AddField(core.FieldMeta{ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}})
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
	if reader.Version() != SegmentVersionV4 || reader.SchemaHash() != "sha256:schema" {
		t.Fatalf("v4 metadata mismatch: version=%d schema=%s", reader.Version(), reader.SchemaHash())
	}
	wildcards := reader.Wildcards()
	if len(wildcards) != 2 || wildcards[0] != 10 || wildcards[1] != 30 {
		t.Fatalf("wildcards mismatch: %v", wildcards)
	}
}

func TestExternalBuilderV4MatchesBuilderAcrossRuns(t *testing.T) {
	build := func(useExternal bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			SetWildcards(core.Entries)
			AddField(core.FieldMeta) error
			AddRecord(string, any, []core.EntryID) error
			Write() error
		}
		if useExternal {
			sink = NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{MaxPostingsInMemory: 1, SchemaHash: "sha256:schema"})
		} else {
			sink = NewInMemorySegmentBuilderWithOptions(buf, InMemorySegmentBuilderOptions{SchemaHash: "sha256:schema"})
		}
		sink.SetDocCount(2)
		sink.SetWildcards(core.Entries{30, 10})
		sink.AddField(core.FieldMeta{ID: 2, Field: "b", FieldOption: core.FieldOption{Encoder: "number"}})
		sink.AddField(core.FieldMeta{ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}})
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
		t.Fatal("external v4 builder bytes mismatch")
	}
}

// TestExternalBuilderWildcardStreamingMatchesBuffered proves the streamed
// wildcard-block-file path (SetWildcardsBlockFile → writeWildcardsFromReader,
// no io.ReadAll) produces a byte-identical segment to the in-memory
// SetWildcards path. This guards the §4.2.2 streaming change against any framing
// or checksum drift.
func TestExternalBuilderWildcardStreamingMatchesBuffered(t *testing.T) {
	wildcards := core.Entries{10, 30, 50, 70}
	sorted := append(core.Entries(nil), wildcards...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	build := func(useBlockFile bool) []byte {
		buf := new(bytes.Buffer)
		tmp := t.TempDir()
		b := NewExternalBuilder(buf, tmp, ExternalBuilderOptions{
			MaxPostingsInMemory: 1,
			SchemaHash:          "sha256:schema",
		})
		b.SetDocCount(2)
		if err := b.AddField(core.FieldMeta{ID: 1, Field: "a", FieldOption: core.FieldOption{Encoder: "number"}}); err != nil {
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
