package segment

import (
	"bytes"
	"strings"
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestFlatPostingList(t *testing.T) {
	entries := []core.EntryID{
		10, 20, 30, 40, 50,
	}

	buf := WriteFlatPostingList(entries)

	pl, err := NewFlatPostingList(buf)
	if err != nil {
		t.Fatal(err)
	}

	if pl.count != 5 {
		t.Fatalf("expected count 5, got %d", pl.count)
	}

	term := core.NewTerm("test", "val")
	cursor := pl.NewPostingCursor(term)

	if cursor.Current() != 10 {
		t.Fatalf("expected 10, got %d", cursor.Current())
	}

	// SkipTo middle
	if e := cursor.SkipTo(25); e != 30 {
		t.Fatalf("expected 30, got %d", e)
	}

	// SkipTo exact
	if e := cursor.SkipTo(40); e != 40 {
		t.Fatalf("expected 40, got %d", e)
	}

	// SkipTo end
	if e := cursor.SkipTo(100); e != core.NULLENTRY {
		t.Fatalf("expected NULLENTRY, got %d", e)
	}
}

// TestFlatPostingListAlignment verifies the writer's alignment invariant:
//   - each posting block starts at a file offset that is a multiple of 8;
//   - the EntryID array starts at block offset 8 (header is 8 bytes);
//
// Together these guarantee an 8-byte aligned uint64 view whenever the segment
// backing memory is 8-byte aligned (os.ReadFile / mmap both return such bases).
func TestFlatPostingListAlignment(t *testing.T) {
	// Build a segment with several terms of varying posting counts so the
	// block layout exercises odd-length lists.
	buf := new(bytes.Buffer)
	w := NewInMemorySegmentBuilder(buf)
	w.AddField(core.FieldMeta{ID: 1, Field: "age"})
	_ = w.AddPosting(1, "age", "18", []core.EntryID{10, 20, 30})
	_ = w.AddPosting(1, "age", "19", []core.EntryID{11})
	_ = w.AddPosting(1, "age", "20", []core.EntryID{12, 22})
	if err := w.Write(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	for blockName, def := range reader.meta.BlockIndex {
		if !strings.HasSuffix(blockName, "_postings") {
			continue
		}
		if def.Offset%8 != 0 {
			t.Fatalf("posting block %s starts at unaligned file offset %d", blockName, def.Offset)
		}
	}

	// Header size invariant: EntryID array begins at offset 8 inside a list.
	pl := WriteFlatPostingList([]core.EntryID{1})
	if len(pl) != postingHeaderSize+8 || postingHeaderSize != 8 {
		t.Fatalf("unexpected posting list header layout: len=%d header=%d", len(pl), postingHeaderSize)
	}
	// Each serialized list length is a multiple of 8 so concatenation preserves alignment.
	if len(pl)%8 != 0 {
		t.Fatalf("posting list length %d not multiple of 8", len(pl))
	}
}
