package segment

import (
	"bytes"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestExternalBuilderMatchesBuilderAcrossRuns(t *testing.T) {
	fields := []core.FieldMeta{{ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}}}
	build := func(useExternal bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			AddField(core.FieldMeta)
			AddPosting(int, string, string, []core.EntryID) error
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
			{term: "2", entry: 30},
			{term: "1", entry: 20},
			{term: "1", entry: 10},
			{term: "2", entry: 40},
			{term: "3", entry: 50},
		}
		for _, posting := range postings {
			if err := sink.AddPosting(1, "a", posting.term, []core.EntryID{posting.entry}); err != nil {
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
	it, err := reader.GetPostingsByTerm(1, "a", "1")
	if err != nil {
		t.Fatal(err)
	}
	if it.Current() != 10 || it.SkipTo(11) != 20 {
		t.Fatalf("posting list order mismatch")
	}
}

func TestExternalBuilderACMatcherAcrossRuns(t *testing.T) {
	buf := new(bytes.Buffer)
	b := NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{MaxPostingsInMemory: 1})
	b.SetDocCount(4)
	b.AddField(core.FieldMeta{
		Field:       core.BEField("keyword"),
		FieldOption: core.FieldOption{Container: core.IndexNameACMatcher},
	})
	postings := []struct {
		term  string
		entry core.EntryID
	}{
		{term: "apple", entry: 2},
		{term: "app", entry: 1},
		{term: "banana", entry: 3},
		{term: "tree", entry: 4},
	}
	for _, posting := range postings {
		if err := b.AddPosting(1, "keyword", posting.term, []core.EntryID{posting.entry}); err != nil {
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
	iters, err := reader.MultiPatternSearch(1, "keyword", "I love apple and banana")
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
	want := []core.EntryID{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("entries length mismatch: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("entries mismatch: got=%v want=%v", got, want)
		}
	}
}

func TestExternalBuilderSegmentV2EmbedsWildcards(t *testing.T) {
	buf := new(bytes.Buffer)
	b := NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{
		MaxPostingsInMemory: 1,
		SchemaHash:          "sha256:schema",
		Wildcards:           core.Entries{30, 10},
	})
	b.SetDocCount(2)
	b.AddField(core.FieldMeta{ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}})
	if err := b.AddPosting(1, "a", "1", []core.EntryID{20}); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(); err != nil {
		t.Fatal(err)
	}
	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if reader.Version() != SegmentVersionV3 || reader.SchemaHash() != "sha256:schema" {
		t.Fatalf("v3 metadata mismatch: version=%d schema=%s", reader.Version(), reader.SchemaHash())
	}
	wildcards := reader.Wildcards()
	if len(wildcards) != 2 || wildcards[0] != 10 || wildcards[1] != 30 {
		t.Fatalf("wildcards mismatch: %v", wildcards)
	}
}

func TestExternalBuilderSegmentV2MatchesBuilderAcrossRuns(t *testing.T) {
	build := func(useExternal bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			SetWildcards(core.Entries)
			AddField(core.FieldMeta)
			AddPosting(int, string, string, []core.EntryID) error
			Write() error
		}
		if useExternal {
			sink = NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{MaxPostingsInMemory: 1, SchemaHash: "sha256:schema"})
		} else {
			sink = NewInMemorySegmentBuilderWithOptions(buf, InMemorySegmentBuilderOptions{SchemaHash: "sha256:schema"})
		}
		sink.SetDocCount(2)
		sink.SetWildcards(core.Entries{30, 10})
		sink.AddField(core.FieldMeta{ID: 2, Field: "b", FieldOption: core.FieldOption{Tokenizer: "number"}})
		sink.AddField(core.FieldMeta{ID: 1, Field: "a", FieldOption: core.FieldOption{Tokenizer: "number"}})
		for _, posting := range []struct {
			term  string
			entry core.EntryID
		}{
			{term: "2", entry: 30},
			{term: "1", entry: 10},
			{term: "1", entry: 20},
		} {
			if err := sink.AddPosting(1, "a", posting.term, []core.EntryID{posting.entry}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.AddPosting(2, "b", "9", []core.EntryID{40}); err != nil {
			t.Fatal(err)
		}
		if err := sink.Write(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	if got, want := build(true), build(false); !bytes.Equal(got, want) {
		t.Fatal("external v2 builder bytes mismatch")
	}
}
