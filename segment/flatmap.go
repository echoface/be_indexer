package segment

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// FlatDict is a minimal memory-mappable term dictionary mapping each term to a
// PostingRef (posting list offset + entry count).
//
// Layout:
//
//	[Count          (uint32)]
//	[TermDataOffset (uint32)] - byte offset where the term string area begins
//	repeated Count times, sorted by term:
//	  [KeyOffset     (uint32)]   - byte offset of the term string within the block
//	  [PostingCount  (uint32)]   - number of EntryIDs in this term's posting list
//	  [PostingOffset (uint64)]   - block-relative offset of the posting list header
//	[TermData string bytes ...]
//
// Carrying PostingCount lets the reader build a posting cursor directly via
// PostingRef without re-reading the posting list count header. Terms are stored
// in lexical order so Find can binary-search and derive each term's end offset
// from the next item's KeyOffset.
type FlatDict struct {
	b     []byte
	count uint32
	items []dictItem
}

const flatDictHeaderSize = 8 // Count(4) + TermDataOffset(4)
const flatDictItemSize = 16  // KeyOffset(4) + PostingCount(4) + PostingOffset(8)

type dictItem struct {
	keyOffset    uint32
	postingCount uint32
	postingOff   uint64
}

// WriteFlatDict serializes a term -> PostingRef map into the FlatDict layout.
func WriteFlatDict(dict map[string]PostingRef) []byte {
	keys := make([]string, 0, len(dict))
	for k := range dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	count := uint32(len(keys))
	itemsAreaSize := int(count) * flatDictItemSize

	stringsSize := 0
	for _, k := range keys {
		stringsSize += len(k)
	}

	buf := make([]byte, flatDictHeaderSize+itemsAreaSize+stringsSize)
	binary.LittleEndian.PutUint32(buf[0:4], count)

	termDataOffset := uint32(flatDictHeaderSize + itemsAreaSize)
	binary.LittleEndian.PutUint32(buf[4:8], termDataOffset)

	curStringOffset := termDataOffset
	for i, key := range keys {
		ref := dict[key]
		itemOffset := flatDictHeaderSize + i*flatDictItemSize
		binary.LittleEndian.PutUint32(buf[itemOffset:itemOffset+4], curStringOffset)
		binary.LittleEndian.PutUint32(buf[itemOffset+4:itemOffset+8], ref.Count)
		binary.LittleEndian.PutUint64(buf[itemOffset+8:itemOffset+16], ref.Offset)

		copy(buf[curStringOffset:], key)
		curStringOffset += uint32(len(key))
	}

	return buf
}

// NewFlatDict creates a dictionary from mapped bytes.
func NewFlatDict(b []byte) (*FlatDict, error) {
	if len(b) < flatDictHeaderSize {
		return nil, fmt.Errorf("truncated flat dict header")
	}

	count := binary.LittleEndian.Uint32(b[0:4])
	if count == 0 {
		return &FlatDict{b: b, count: 0}, nil
	}

	termDataOffset := binary.LittleEndian.Uint32(b[4:8])
	expectedMinLen := flatDictHeaderSize + int(count)*flatDictItemSize
	if len(b) < expectedMinLen || len(b) < int(termDataOffset) {
		return nil, fmt.Errorf("truncated flat dict data")
	}

	items := make([]dictItem, count)
	for i := 0; i < int(count); i++ {
		offset := flatDictHeaderSize + i*flatDictItemSize
		items[i] = dictItem{
			keyOffset:    binary.LittleEndian.Uint32(b[offset : offset+4]),
			postingCount: binary.LittleEndian.Uint32(b[offset+4 : offset+8]),
			postingOff:   binary.LittleEndian.Uint64(b[offset+8 : offset+16]),
		}
	}

	return &FlatDict{
		b:     b,
		count: count,
		items: items,
	}, nil
}

// Find returns the PostingRef for the given term, or false if not found.
func (d *FlatDict) Find(term []byte) (PostingRef, bool) {
	if d.count == 0 {
		return PostingRef{}, false
	}

	left := 0
	right := int(d.count) - 1

	for left <= right {
		mid := left + (right-left)/2
		item := d.items[mid]

		keyStart := item.keyOffset
		keyEnd := uint32(len(d.b))
		if mid < int(d.count)-1 {
			keyEnd = d.items[mid+1].keyOffset
		}

		if keyStart > keyEnd || keyEnd > uint32(len(d.b)) {
			return PostingRef{}, false
		}

		keyBytes := d.b[keyStart:keyEnd]
		cmp := bytes.Compare(keyBytes, term)
		if cmp == 0 {
			return PostingRef{Offset: item.postingOff, Count: item.postingCount}, true
		} else if cmp < 0 {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}

	return PostingRef{}, false
}
