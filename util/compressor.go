package util

import (
	"fmt"
	"slices"
	"sort"
)

const (
	DefaultBlockSize = 128
)

// CompHeader contains metadata for a compressed block.
type CompHeader struct {
	LastEntryID uint64
	Offset      uint32
}

// Compressor helps compress sorted posting lists (entries).
type Compressor struct {
	blockSize      int
	headers        []CompHeader
	compressedData []byte
}

func NewCompressor(blockSize int) *Compressor {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	return &Compressor{
		blockSize:      blockSize,
		headers:        make([]CompHeader, 0),
		compressedData: make([]byte, 0, 1024),
	}
}

// AddPostingList compresses a single posting list (which must be sorted)
// and appends to the buffer.
// Returns the starting offset in the Headers slice.
func (c *Compressor) AddPostingList(entries []uint64) int {
	return AddPostingList(c, entries)
}

// GetHeaders returns a view of internal headers slice.
// NOTE: The returned slice MUST be treated as read-only and is not safe for concurrent
// mutation. If you need an immutable snapshot, use HeadersCopy.
func (c *Compressor) GetHeaders() []CompHeader {
	return c.headers
}

// HeadersCopy returns a copy of headers.
func (c *Compressor) HeadersCopy() []CompHeader {
	return slices.Clone(c.headers)
}

// GetCompressedData returns a view of internal compressed data.
// NOTE: The returned slice MUST be treated as read-only and is not safe for concurrent
// mutation. If you need an immutable snapshot, use CompressedDataCopy.
func (c *Compressor) GetCompressedData() []byte {
	return c.compressedData
}

// CompressedDataCopy returns a copy of compressed data.
func (c *Compressor) CompressedDataCopy() []byte {
	return slices.Clone(c.compressedData)
}

// KeyEntry represents a key in the flat map.
type KeyEntry[K any] struct {
	Key         K
	HeaderIndex uint32
	// 该 key 对应的 posting list（entries 列表）里元素的个数（也就是 docID/EntryID 的数量）
	Length uint32
}

// FlatMap represents the compressed map structure.
type FlatMap[K any] struct {
	Keys    []KeyEntry[K]
	Headers []CompHeader
	Data    []byte
}

// BuildFlatMap takes a map of generic keys to entries (as uint64 slice),
// sorts the keys, compresses the entries, and returns the flat map components.
// Note: The input entries in the map will be sorted in place.
func BuildFlatMap[K comparable, V ~uint64, S ~[]V](data map[K]S, less func(i, j K) bool, blockSize int) *FlatMap[K] {
	// 1. Collect and sort keys
	keys := make([]K, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return less(keys[i], keys[j])
	})

	// 2. Compress entries
	compressor := NewCompressor(blockSize)
	sortedKeys := make([]KeyEntry[K], len(keys))

	for i, key := range keys {
		entries := data[key]

		// Ensure entries are sorted
		slices.Sort(entries)

		headerStart := AddPostingList(compressor, entries)
		if headerStart < 0 {
			panic(fmt.Sprintf("be_indexer: invalid headerStart=%d", headerStart))
		}
		if uint64(headerStart) > uint64(^uint32(0)) {
			panic(fmt.Sprintf("be_indexer: too many headers: headerStart=%d exceeds uint32", headerStart))
		}
		if uint64(len(entries)) > uint64(^uint32(0)) {
			panic(fmt.Sprintf("be_indexer: posting list too large: len=%d exceeds uint32", len(entries)))
		}

		sortedKeys[i] = KeyEntry[K]{
			Key:         key,
			HeaderIndex: uint32(headerStart),
			Length:      uint32(len(entries)),
		}
	}
	return &FlatMap[K]{
		Keys:    sortedKeys,
		Headers: compressor.GetHeaders(),
		Data:    compressor.GetCompressedData(),
	}
}

// AddPostingList generic helper
func AddPostingList[V ~uint64](c *Compressor, entries []V) int {
	if c == nil {
		panic("be_indexer: nil compressor")
	}
	// Defensive: avoid hang if blockSize was misconfigured after construction.
	if c.blockSize <= 0 {
		c.blockSize = DefaultBlockSize
	}
	headerStart := len(c.headers)
	if len(entries) == 0 {
		return headerStart
	}
	if uint64(headerStart) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: too many headers: headerStart=%d exceeds uint32", headerStart))
	}
	if uint64(len(entries)) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: posting list too large: len=%d exceeds uint32", len(entries)))
	}

	for j := 0; j < len(entries); j += c.blockSize {
		end := min(j+c.blockSize, len(entries))
		blockEntries := entries[j:end]
		if len(blockEntries) == 0 {
			// Should be unreachable due to loop bounds, but keep for safety.
			continue
		}
		if uint64(len(c.compressedData)) > uint64(^uint32(0)) {
			panic(fmt.Sprintf("be_indexer: compressed data too large: len=%d exceeds uint32", len(c.compressedData)))
		}

		header := CompHeader{
			LastEntryID: uint64(blockEntries[len(blockEntries)-1]),
			Offset:      uint32(len(c.compressedData)),
		}
		c.headers = append(c.headers, header)

		// Encode block
		base := uint64(blockEntries[0])
		c.compressedData = AppendVarint(c.compressedData, base)

		for k := 1; k < len(blockEntries); k++ {
			curr := uint64(blockEntries[k])
			if curr < base {
				panic(fmt.Sprintf("be_indexer: posting list must be sorted non-decreasing, got %d then %d", base, curr))
			}
			delta := curr - base
			c.compressedData = AppendVarint(c.compressedData, delta)
			base = curr
		}
	}
	return headerStart
}

// Ordered represents types that support <, >, etc.
type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64 |
		~string
}

// FlatMapBuilder helps build a flat map iteratively to save memory.
type FlatMapBuilder[K comparable, V ~uint64] struct {
	compressor *Compressor
	keys       []KeyEntry[K]
	less       func(i, j K) bool
}

func NewFlatMapBuilder[K comparable, V ~uint64](blockSize int, less func(i, j K) bool) *FlatMapBuilder[K, V] {
	return &FlatMapBuilder[K, V]{
		compressor: NewCompressor(blockSize),
		keys:       make([]KeyEntry[K], 0),
		less:       less,
	}
}

// Add appends a key and its entries.
// NOTE: Entries will be sorted in-place.
func (b *FlatMapBuilder[K, V]) Add(key K, entries []V) {
	// Ensure entries are sorted
	slices.Sort(entries)

	headerStart := AddPostingList(b.compressor, entries)
	if headerStart < 0 {
		panic(fmt.Sprintf("be_indexer: invalid headerStart=%d", headerStart))
	}
	if uint64(headerStart) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: too many headers: headerStart=%d exceeds uint32", headerStart))
	}
	if uint64(len(entries)) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: posting list too large: len=%d exceeds uint32", len(entries)))
	}

	b.keys = append(b.keys, KeyEntry[K]{
		Key:         key,
		HeaderIndex: uint32(headerStart),
		Length:      uint32(len(entries)),
	})
}

// AddSorted appends a key and its entries.
// NOTE: entries MUST already be sorted non-decreasing.
func (b *FlatMapBuilder[K, V]) AddSorted(key K, entries []V) {
	headerStart := AddPostingList(b.compressor, entries)
	if headerStart < 0 {
		panic(fmt.Sprintf("be_indexer: invalid headerStart=%d", headerStart))
	}
	if uint64(headerStart) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: too many headers: headerStart=%d exceeds uint32", headerStart))
	}
	if uint64(len(entries)) > uint64(^uint32(0)) {
		panic(fmt.Sprintf("be_indexer: posting list too large: len=%d exceeds uint32", len(entries)))
	}

	b.keys = append(b.keys, KeyEntry[K]{
		Key:         key,
		HeaderIndex: uint32(headerStart),
		Length:      uint32(len(entries)),
	})
}

// Build sorts the keys and returns the components.
// It can be called multiple times, but usually called once at the end.
func (b *FlatMapBuilder[K, V]) Build() *FlatMap[K] {
	// Sort keys
	sort.Slice(b.keys, func(i, j int) bool {
		return b.less(b.keys[i].Key, b.keys[j].Key)
	})

	// Return immutable snapshots to avoid data corruption if the builder is reused.
	keys := slices.Clone(b.keys)
	headers := b.compressor.HeadersCopy()
	data := b.compressor.CompressedDataCopy()
	return &FlatMap[K]{
		Keys:    keys,
		Headers: headers,
		Data:    data,
	}
}

// NewFlatMapBuilderOrdered is a helper for ordered keys.
func NewFlatMapBuilderOrdered[K Ordered, V ~uint64](blockSize int) *FlatMapBuilder[K, V] {
	return NewFlatMapBuilder[K, V](blockSize, func(i, j K) bool {
		return i < j
	})
}
