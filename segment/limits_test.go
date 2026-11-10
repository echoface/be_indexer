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
	// On 32-bit hosts MaxBlockSize is already MaxInt, so no larger int exists.
	maxBlock := int(MaxBlockSize)
	if maxBlock < int(^uint(0)>>1) {
		if _, err := checkedU32(maxBlock+1, "x"); err == nil {
			t.Fatal("expected overflow error for MaxBlockSize+1")
		}
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

// --- EncodeFlatPostingList / EncodeFlatDict contract ------------------------------

// A normal-sized posting list must serialize without error and round-trip.
func TestEncodeFlatPostingList_ValidRoundTrip(t *testing.T) {
	entries := []core.EntryID{1, 5, 9, 42}
	buf, err := EncodeFlatPostingList(entries)
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
func TestEncodeFlatPostingList_Empty(t *testing.T) {
	buf, err := EncodeFlatPostingList(nil)
	if err != nil {
		t.Fatalf("empty list must not error: %v", err)
	}
	if len(buf) != postingHeaderSize {
		t.Fatalf("empty list should be header-only, got %d bytes", len(buf))
	}
}

func TestEncodeFlatDict_ValidRoundTrip(t *testing.T) {
	dict := map[string]PostingRef{
		"apple":  {Offset: 0, Count: 1},
		"banana": {Offset: 16, Count: 2},
	}
	buf, err := EncodeFlatDict(dict)
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
