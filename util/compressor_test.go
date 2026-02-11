package util

import (
	"testing"
)

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic, got none")
		}
	}()
	fn()
}

func TestCompressor(t *testing.T) {
	entries := []uint64{1, 5, 10, 100, 101, 102, 200, 500}
	
	c := NewCompressor(4) // BlockSize = 4
	offset := c.AddPostingList(entries)
	
	if offset != 0 {
		t.Errorf("expected offset 0, got %d", offset)
	}
	
	headers := c.GetHeaders()
	if len(headers) != 2 { // 8 entries / 4 = 2 blocks
		t.Errorf("expected 2 headers, got %d", len(headers))
	}
	
	// Block 1: 1, 5, 10, 100
	if headers[0].LastEntryID != 100 {
		t.Errorf("expected header 0 lastID 100, got %d", headers[0].LastEntryID)
	}
	
	// Block 2: 101, 102, 200, 500
	if headers[1].LastEntryID != 500 {
		t.Errorf("expected header 1 lastID 500, got %d", headers[1].LastEntryID)
	}
}

func TestCompressor_EmptyEntries_NoPanic(t *testing.T) {
	c := NewCompressor(4)
	offset := c.AddPostingList(nil)
	if offset != 0 {
		t.Fatalf("expected offset 0, got %d", offset)
	}
	if len(c.GetHeaders()) != 0 {
		t.Fatalf("expected 0 headers, got %d", len(c.GetHeaders()))
	}
	if len(c.GetCompressedData()) != 0 {
		t.Fatalf("expected 0 data, got %d", len(c.GetCompressedData()))
	}
}

func TestCompressor_BlockSizeZero_NoHang(t *testing.T) {
	// Simulate misconfigured compressor after construction.
	c := &Compressor{blockSize: 0}
	offset := AddPostingList(c, []uint64{1, 2, 3})
	if offset != 0 {
		t.Fatalf("expected offset 0, got %d", offset)
	}
	if len(c.headers) == 0 || len(c.compressedData) == 0 {
		t.Fatalf("expected headers and data to be written")
	}
}

func TestCompressor_UnsortedEntries_ShouldPanic(t *testing.T) {
	c := NewCompressor(4)
	mustPanic(t, func() {
		_ = c.AddPostingList([]uint64{2, 1})
	})
}

func TestBuildFlatMap(t *testing.T) {
	data := map[int][]uint64{
		10: {1, 5},
		5:  {2, 3},
		20: {10},
	}
	
	less := func(i, j int) bool { return i < j }
	
	fm := BuildFlatMap(data, less, 128)
	
	if len(fm.Keys) != 3 {
		t.Errorf("expected 3 keys")
	}
	
	// Order: 5, 10, 20
	if fm.Keys[0].Key != 5 {
		t.Errorf("expected key[0]=5")
	}
	if fm.Keys[1].Key != 10 {
		t.Errorf("expected key[1]=10")
	}
	if fm.Keys[2].Key != 20 {
		t.Errorf("expected key[2]=20")
	}
	
	if len(fm.Headers) != 3 { // 3 lists, each small enough for 1 block
		t.Errorf("expected 3 headers, got %d", len(fm.Headers))
	}
	
	if len(fm.Data) == 0 {
		t.Errorf("expected compressed data")
	}
}

func TestFlatMapBuilder(t *testing.T) {
	builder := NewFlatMapBuilderOrdered[int, uint64](128)
	
	// Add in random order
	builder.Add(10, []uint64{1, 5})
	builder.Add(5, []uint64{2, 3})
	builder.Add(20, []uint64{10})
	
	fm := builder.Build()
	
	if len(fm.Keys) != 3 {
		t.Errorf("expected 3 keys")
	}
	
	// Result should be sorted by Key
	if fm.Keys[0].Key != 5 {
		t.Errorf("expected key[0]=5")
	}
	if fm.Keys[1].Key != 10 {
		t.Errorf("expected key[1]=10")
	}
	if fm.Keys[2].Key != 20 {
		t.Errorf("expected key[2]=20")
	}
	
	if len(fm.Headers) != 3 {
		t.Errorf("expected 3 headers")
	}
	
	if len(fm.Data) == 0 {
		t.Errorf("expected compressed data")
	}
}
