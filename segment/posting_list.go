package segment

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"unsafe"

	"github.com/echoface/be_indexer/core"
)

// FlatPostingList represents a memory-mapped array of EntryIDs.
//
// Layout (v2, 8-byte aligned):
//
//	[Count (uint32)]
//	[Pad   (uint32) = 0]   // makes the EntryID array start at offset 8
//	[EntryID_1 (uint64)]
//	[EntryID_2 (uint64)]
//	...
//
// The 4-byte pad guarantees two properties required for safe zero-copy mapping:
//  1. The EntryID array begins at offset 8 inside the block, so when the block
//     itself starts on an 8-byte boundary the uint64 view is naturally aligned.
//  2. The total serialized length is always 8 + 8*N (a multiple of 8), so when
//     multiple posting lists are concatenated inside one block each subsequent
//     list also stays 8-byte aligned.
//
// The previous layout placed the array at offset 4, which can never be 8-byte
// aligned (an 8-aligned base + 4 is always 4-aligned), violating the unsafe
// alignment contract and tripping -race/checkptr on some platforms.
const postingHeaderSize = 8

// PostingRef locates a posting list inside a posting block: Offset is the byte
// offset of the list's header within the block, Count is the number of EntryIDs.
// It is the single physical handle shared by the dictionary (FlatDict) and the
// AC automaton output, so neither has to re-parse the posting header or do a
// second dictionary lookup on the query hot path.
type PostingRef struct {
	Offset uint64
	Count  uint32
}

type FlatPostingList struct {
	count uint32
	data  []core.EntryID
}

// NewPostingListAt maps the posting list of `count` EntryIDs whose header starts
// at block-relative `offset` inside `block`. It trusts the count carried by the
// caller (dict/AC ref), skipping the on-block count header read while still
// validating bounds.
func NewPostingListAt(block []byte, ref PostingRef) (*FlatPostingList, error) {
	if ref.Count == 0 {
		return &FlatPostingList{count: 0}, nil
	}
	start := ref.Offset + postingHeaderSize
	end := start + uint64(ref.Count)*8
	if end > uint64(len(block)) {
		return nil, fmt.Errorf("posting ref out of range: offset=%d count=%d block=%d", ref.Offset, ref.Count, len(block))
	}
	data := mapEntryIDs(block[start:end], ref.Count)
	return &FlatPostingList{count: ref.Count, data: data}, nil
}

// NewFlatPostingList creates a FlatPostingList from a raw byte slice.
func NewFlatPostingList(b []byte) (*FlatPostingList, error) {
	if len(b) < postingHeaderSize {
		return nil, fmt.Errorf("invalid flat posting list size: %d", len(b))
	}
	count := binary.LittleEndian.Uint32(b[0:4])
	if count == 0 {
		return &FlatPostingList{count: 0}, nil
	}

	expectedLen := postingHeaderSize + int(count)*8
	if len(b) < expectedLen {
		return nil, fmt.Errorf("truncated posting list: expected %d bytes, got %d", expectedLen, len(b))
	}

	data := mapEntryIDs(b[postingHeaderSize:expectedLen], count)
	return &FlatPostingList{
		count: count,
		data:  data,
	}, nil
}

// mapEntryIDs returns a []core.EntryID view over raw. It uses a zero-copy
// unsafe mapping when the backing memory is 8-byte aligned (the normal path for
// a correctly aligned segment), and falls back to a decoded copy otherwise so
// that callers never construct a misaligned uint64 slice.
func mapEntryIDs(raw []byte, count uint32) []core.EntryID {
	ptr := unsafe.Pointer(&raw[0])
	if uintptr(ptr)&7 == 0 {
		return unsafe.Slice((*core.EntryID)(ptr), count)
	}
	// Misaligned fallback: decode explicitly. This allocates, but only triggers
	// when a block was not written through the aligned writer path.
	data := make([]core.EntryID, count)
	for i := uint32(0); i < count; i++ {
		data[i] = core.EntryID(binary.LittleEndian.Uint64(raw[i*8 : i*8+8]))
	}
	return data
}

// NewPostingCursor creates an iterator over the mapped posting list.
func (pl *FlatPostingList) NewPostingCursor(term core.Term) core.PostingIterator {
	return &flatPostingCursor{
		term: term,
		pl:   pl,
		idx:  0,
	}
}

// WriteFlatPostingList serializes a slice of EntryIDs into flat bytes.
func WriteFlatPostingList(entries []core.EntryID) []byte {
	if len(entries) > math.MaxUint32 {
		panic(fmt.Errorf("posting list too large: %d entries exceeds uint32 count", len(entries)))
	}
	maxInt := int(^uint(0) >> 1)
	if len(entries) > (maxInt-postingHeaderSize)/8 {
		panic(fmt.Errorf("posting list too large: %d entries exceeds addressable buffer size", len(entries)))
	}
	count := uint32(len(entries))
	buf := make([]byte, postingHeaderSize+len(entries)*8)
	binary.LittleEndian.PutUint32(buf[0:4], count)
	// buf[4:8] stays zero padding so the EntryID array starts at offset 8.
	for i, e := range entries {
		off := postingHeaderSize + i*8
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(e))
	}
	return buf
}

// flatPostingCursor implements core.PostingIterator
type flatPostingCursor struct {
	term core.Term
	pl   *FlatPostingList
	idx  uint32
}

func (c *flatPostingCursor) Current() core.EntryID {
	if c.idx >= c.pl.count {
		return core.NULLENTRY
	}
	return c.pl.data[c.idx]
}

func (c *flatPostingCursor) SkipTo(target core.EntryID) core.EntryID {
	if c.idx >= c.pl.count {
		return core.NULLENTRY
	}

	// Galloping search: exponential probe for sequential-dominant patterns.
	if c.pl.data[c.idx] >= target {
		return c.pl.data[c.idx]
	}

	lo := c.idx + 1
	hi := lo
	var step uint32 = 1
	for hi < c.pl.count && c.pl.data[hi] < target {
		lo = hi + 1
		step *= 2
		hi = c.idx + step
	}
	if hi >= c.pl.count {
		hi = c.pl.count - 1
	}

	// Semi-open [lo, hi+1) binary search — safe from uint32 underflow.
	left := lo
	right := hi + 1
	for left < right {
		mid := left + (right-left)/2
		if c.pl.data[mid] < target {
			left = mid + 1
		} else {
			right = mid
		}
	}

	c.idx = left
	if c.idx >= c.pl.count {
		return core.NULLENTRY
	}
	return c.pl.data[c.idx]
}

func (c *flatPostingCursor) Term() core.Term {
	return c.term
}

func (c *flatPostingCursor) ReachEnd() bool {
	return c.idx >= c.pl.count
}

// CountUint32 returns the number of EntryIDs in this posting list as uint32.
func (pl *FlatPostingList) CountUint32() uint32 { return pl.count }

// Count returns the number of EntryIDs as int.
func (pl *FlatPostingList) Count() int { return int(pl.count) }

// EntryIndex returns the i-th EntryID. Caller must ensure i < int(pl.count).
func (pl *FlatPostingList) EntryIndex(i uint32) core.EntryID { return pl.data[i] }

// SubView returns a zero-copy sub-posting-list of entries in [start, end).
func (pl *FlatPostingList) SubView(start, end int) *FlatPostingList {
	if start >= end {
		return &FlatPostingList{count: 0}
	}
	return &FlatPostingList{count: uint32(end - start), data: pl.data[start:end]}
}

// SubViewByK returns a zero-copy sub-posting-list containing only entries
// with conjunction size K. It uses binary search to locate the K-range
// boundaries within the EntryID-sorted posting list.
func (pl *FlatPostingList) SubViewByK(k int) *FlatPostingList {
	count := int(pl.count)
	start := sort.Search(count, func(i int) bool { return pl.data[i] >= core.KStartEntryID(k) })
	end := sort.Search(count, func(i int) bool { return pl.data[i] >= core.KStartEntryID(k+1) })
	return pl.SubView(start, end)
}
