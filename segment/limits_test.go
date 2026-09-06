package segment

import (
	"testing"

	"github.com/echoface/be_indexer/core"
)

// --- helper limit checks --------------------------------------------------------

func TestCheckedU32(t *testing.T) {
	if _, err := checkedU32(0, "x"); err != nil {
		t.Fatalf("0 must be valid: %v", err)
	}
	if _, err := checkedU32(MaxBlockSize, "x"); err != nil {
		t.Fatalf("max must be valid: %v", err)
	}
	if _, err := checkedU32(MaxBlockSize+1, "x"); err == nil {
		t.Fatal("expected overflow error for MaxBlockSize+1")
	}
	if _, err := checkedU32(-1, "x"); err == nil {
		t.Fatal("expected error for negative")
	}
}

func TestCheckedU16(t *testing.T) {
	if _, err := checkedU16(MaxACOutputPerState, "x"); err != nil {
		t.Fatalf("max must be valid: %v", err)
	}
	if _, err := checkedU16(MaxACOutputPerState+1, "x"); err == nil {
		t.Fatal("expected overflow error for MaxACOutputPerState+1")
	}
}

// --- WriteFlatPostingList / WriteFlatDict contract ------------------------------

// A normal-sized posting list must serialize without error and round-trip.
func TestWriteFlatPostingList_ValidRoundTrip(t *testing.T) {
	entries := []core.EntryID{1, 5, 9, 42}
	buf, err := WriteFlatPostingList(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pl, err := NewFlatPostingList(buf)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	if pl.count != uint32(len(entries)) {
		t.Fatalf("count mismatch: got %d want %d", pl.count, len(entries))
	}
}

// An empty posting list is valid (count 0) and must not error or panic.
func TestWriteFlatPostingList_Empty(t *testing.T) {
	buf, err := WriteFlatPostingList(nil)
	if err != nil {
		t.Fatalf("empty list must not error: %v", err)
	}
	if len(buf) != postingHeaderSize {
		t.Fatalf("empty list should be header-only, got %d bytes", len(buf))
	}
}

func TestWriteFlatDict_ValidRoundTrip(t *testing.T) {
	dict := map[string]PostingRef{
		"apple":  {Offset: 0, Count: 1},
		"banana": {Offset: 16, Count: 2},
	}
	buf, err := WriteFlatDict(dict)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fd, err := NewFlatDict(buf)
	if err != nil {
		t.Fatalf("round-trip decode failed: %v", err)
	}
	if _, ok := fd.Find([]byte("banana")); !ok {
		t.Fatal("expected to find banana")
	}
}

// --- dense field id ceiling -----------------------------------------------------

// The dense-field-id guard is validated in SegmentReader construction. Building
// a segment with >65536 fields is impractical here, so we assert the guard
// constant is wired to the uint16 ceiling (a compile-time-ish invariant check
// that documents intent and fails if someone widens/narrows it accidentally).
func TestDenseFieldIDLimitConstant(t *testing.T) {
	if MaxDenseFieldID != 0xFFFF {
		t.Fatalf("MaxDenseFieldID must be the uint16 ceiling, got %d", MaxDenseFieldID)
	}
}
