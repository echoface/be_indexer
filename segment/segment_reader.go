package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	mmapgo "github.com/blevesearch/mmap-go"

	"github.com/echoface/be_indexer/core"
)

// blockKey is a dense field id used for block lookup.
// blockKey resolution is O(1) via integer map key, avoiding hashing a field
// string on the retrieval hot path.
type blockKey uint16

func makeBlockKey(fieldID uint16) blockKey {
	return blockKey(fieldID)
}

// blockLookup holds pre-resolved references for a field (all K values merged).
type blockLookup struct {
	dict       *FlatDict
	pl         []byte
	containers map[string]ContainerReader
}

type BlockChecksumMode int

const (
	// BlockChecksumOnOpen verifies every block while opening the reader. This is
	// the strictest mode and remains the default for direct NewSegmentReader calls and
	// tests that validate corruption handling.
	BlockChecksumOnOpen BlockChecksumMode = iota
	// BlockChecksumDisabled trusts the caller/manifest-level file checksum and
	// skips the O(segment_size) block checksum scan during reader construction.
	// This is useful for serving paths where cold-start latency matters more than
	// duplicate validation.
	BlockChecksumDisabled
)

// ReaderOptions controls validation performed when constructing a segment reader.
type ReaderOptions struct {
	BlockChecksumMode BlockChecksumMode
}

// SegmentReader serves queries from an immutable segment byte slice. The byte slice
// may be backed by OS mmap or ordinary memory; all posting-list lookups are
// zero-copy views into that backing slice when alignment permits.
type SegmentReader struct {
	b         []byte
	meta      *MetaBlock
	blocks    map[blockKey]*blockLookup
	fieldID   map[string]uint16 // field name -> dense id used in blockKey
	wildcards core.Entries
	// closer releases the backing storage (e.g. munmap). It is nil for readers
	// over caller-owned byte slices, whose memory is reclaimed by the GC.
	closer func() error
}

// NewSegmentReader parses an immutable segment byte slice using strict block
// checksum validation. Use NewSegmentReaderWithOptions for serving paths that have
// already verified the whole file and want to skip the extra block scan.
func NewSegmentReader(b []byte) (*SegmentReader, error) {
	return NewSegmentReaderWithOptions(b, ReaderOptions{BlockChecksumMode: BlockChecksumOnOpen})
}

// NewSegmentReaderWithOptions parses an immutable segment byte slice.
func NewSegmentReaderWithOptions(b []byte, opts ReaderOptions) (*SegmentReader, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("truncated segment file")
	}

	magic := b[:8]
	if string(magic) != string(MagicNumber) {
		return nil, fmt.Errorf("invalid magic number")
	}

	metaOffset := binary.LittleEndian.Uint64(b[len(b)-8:])
	if metaOffset > uint64(len(b)-8) {
		return nil, fmt.Errorf("invalid metadata offset %d", metaOffset)
	}

	metaBytes := b[metaOffset : len(b)-8]
	var meta MetaBlock
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}
	if meta.Version != SegmentVersionV4 {
		return nil, fmt.Errorf("unsupported segment version %d (expected v4)", meta.Version)
	}

	blocks := make(map[blockKey]*blockLookup)
	var wildcards core.Entries
	if opts.BlockChecksumMode == BlockChecksumOnOpen {
		if err := verifyBlockChecksums(b, metaOffset, meta.BlockIndex); err != nil {
			return nil, err
		}
	} else if err := verifyBlockChecksumMetadata(meta.BlockIndex); err != nil {
		return nil, err
	}
	if meta.WildcardsBlock == "" {
		return nil, fmt.Errorf("wildcards block is required")
	}
	blockDef, ok := meta.BlockIndex[meta.WildcardsBlock]
	if !ok {
		return nil, fmt.Errorf("wildcards block %q not found", meta.WildcardsBlock)
	}
	blockBytes, err := checkedBlockBytes(b, metaOffset, blockDef)
	if err != nil {
		return nil, fmt.Errorf("invalid block %s: %w", meta.WildcardsBlock, err)
	}
	wildcards, err = decodeEntriesBlock(blockBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse wildcards block: %w", err)
	}

	// Assign a dense field id per declared field for the integer blockKey.
	fieldID := make(map[string]uint16, len(meta.Fields))
	for _, fieldMeta := range meta.Fields {
		if _, ok := fieldID[fieldMeta.Name]; !ok {
			fieldID[fieldMeta.Name] = uint16(len(fieldID))
		}
	}

	// Single O(blocks) pass: structured BlockDef carries (Field, kind).
	for name, def := range meta.BlockIndex {
		if def.Kind == BlockKindWildcards {
			continue // already decoded above
		}
		fid, ok := fieldID[def.Field]
		if !ok {
			return nil, fmt.Errorf("block %s references unknown field %q", name, def.Field)
		}
		key := makeBlockKey(fid)
		blk, exists := blocks[key]
		if !exists {
			blk = &blockLookup{}
			blocks[key] = blk
		}

		blockBytes, err := checkedBlockBytes(b, metaOffset, def)
		if err != nil {
			return nil, fmt.Errorf("invalid block %s: %w", name, err)
		}
		switch def.Kind {
		case BlockKindDict:
			dict, err := NewFlatDict(blockBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse dict for %s: %w", name, err)
			}
			blk.dict = dict
		case BlockKindPostings:
			blk.pl = blockBytes
		default:
			cr, err := NewContainerReader(def.Kind, blockBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to load container %q for %s: %w", def.Kind, name, err)
			}
			if blk.containers == nil {
				blk.containers = make(map[string]ContainerReader)
			}
			blk.containers[def.Kind] = cr
		}
	}

	// Ensure fields with a dict get a DictIndex with the loaded dict
	// so ContainerQuery("default", ...) works for the default path.
	for _, blk := range blocks {
		if blk.dict != nil {
			if blk.containers == nil {
				blk.containers = make(map[string]ContainerReader)
			}
			if _, ok := blk.containers[core.IndexNameDefault]; !ok {
				blk.containers[core.IndexNameDefault] = NewDictReader(blk.dict)
			}
		}
	}

	return &SegmentReader{
		b:         b,
		meta:      &meta,
		blocks:    blocks,
		fieldID:   fieldID,
		wildcards: wildcards,
	}, nil
}

// OpenSegmentFile memory-maps a segment file read-only and returns a reader over
// the mapped region. The returned reader owns the mapping: call Close to unmap
// it. A finalizer also unmaps on GC as a safety net, so a forgotten Close leaks
// the mapping until collection instead of crashing in-flight zero-copy queries.
//
// Prefer this over reading the whole file into the heap for serving paths: the
// OS page cache is shared across readers/processes and only touched pages count
// toward RSS. Because mmap defers paging, BlockChecksumDisabled is recommended
// (a full block checksum scan would fault in every page, defeating mmap).
func OpenSegmentFile(path string, opts ReaderOptions) (*SegmentReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := mmapgo.Map(f, mmapgo.RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("mmap %s: %w", path, err)
	}

	reader, err := NewSegmentReaderWithOptions(data, opts)
	if err != nil {
		_ = data.Unmap()
		return nil, err
	}
	reader.closer = data.Unmap
	runtime.SetFinalizer(reader, func(sr *SegmentReader) { _ = sr.Close() })
	return reader, nil
}

func verifyBlockChecksumMetadata(blocks map[string]BlockDef) error {
	if len(blocks) == 0 {
		return fmt.Errorf("segment requires at least one block")
	}
	for name, def := range blocks {
		if def.Checksum == "" {
			return fmt.Errorf("block %s checksum missing", name)
		}
	}
	return nil
}

func verifyBlockChecksums(b []byte, metaOffset uint64, blocks map[string]BlockDef) error {
	if err := verifyBlockChecksumMetadata(blocks); err != nil {
		return err
	}
	for name, def := range blocks {
		blockBytes, err := checkedBlockBytes(b, metaOffset, def)
		if err != nil {
			return fmt.Errorf("invalid block %s: %w", name, err)
		}
		sum := sha256.Sum256(blockBytes)
		got := checksumPrefix + hex.EncodeToString(sum[:])
		if def.Checksum != got {
			return fmt.Errorf("block %s checksum mismatch", name)
		}
	}
	return nil
}

func checkedBlockBytes(b []byte, metaOffset uint64, blockDef BlockDef) ([]byte, error) {
	if blockDef.Size == 0 {
		return nil, fmt.Errorf("empty block")
	}
	if blockDef.Offset > metaOffset {
		return nil, fmt.Errorf("offset %d exceeds metadata offset %d", blockDef.Offset, metaOffset)
	}
	if blockDef.Size > metaOffset-blockDef.Offset {
		return nil, fmt.Errorf("offset %d size %d exceeds data region %d", blockDef.Offset, blockDef.Size, metaOffset)
	}
	return b[blockDef.Offset : blockDef.Offset+blockDef.Size], nil
}

// lookupBlock resolves the pre-parsed block group for the given field via the
// dense field id, avoiding a string-keyed map lookup on the retrieval hot path.
func (sr *SegmentReader) lookupBlock(field core.BEField) (*blockLookup, bool) {
	fid, ok := sr.fieldID[string(field)]
	if !ok {
		return nil, false
	}
	blk, ok := sr.blocks[makeBlockKey(fid)]
	return blk, ok
}

// GetPostingsByTerm returns a posting iterator for a physical term in the
// merged (all-K) posting list. This is a convenience wrapper around
// ContainerQuery with the default dict container.
func (sr *SegmentReader) GetPostingsByTerm(field core.BEField, term string) (core.PostingIterator, error) {
	iters, err := sr.ContainerQuery(field, core.IndexNameDefault, term)
	if err != nil {
		return nil, err
	}
	if len(iters) == 0 {
		return nil, nil
	}
	return iters[0], nil
}

// ContainerQuery dispatches a query to a registered container and returns
// posting iterators. kind is the container type (e.g. "default",
// "ac_matcher", "ext_range").
func (sr *SegmentReader) ContainerQuery(field core.BEField, kind string, query interface{}) ([]core.PostingIterator, error) {
	blk, ok := sr.lookupBlock(field)
	if !ok {
		return nil, core.ErrUnknownQueryField
	}
	cr, ok := blk.containers[kind]
	if !ok {
		return nil, nil
	}
	ctx := BlockContext{Pl: blk.pl}
	return cr.MatchQuery(ctx, field, query)
}

// MultiPatternSearch performs AC automaton matching on the input text and
// returns posting iterators for all matched terms in the merged posting list.
func (sr *SegmentReader) MultiPatternSearch(field core.BEField, text string) ([]core.PostingIterator, error) {
	return sr.ContainerQuery(field, BlockKindAC, text)
}

// GetRangePostings returns posting iterators for every interval that contains
// the query point in an ext_range field, from the merged (all-K) posting list.
func (sr *SegmentReader) GetRangePostings(field core.BEField, point int64) ([]core.PostingIterator, error) {
	return sr.ContainerQuery(field, BlockKindRange, point)
}

// SchemaHash returns the embedded schema hash for segment v4 files.
func (sr *SegmentReader) SchemaHash() string {
	if sr == nil || sr.meta == nil {
		return ""
	}
	return sr.meta.SchemaHash
}

// Version returns the physical segment format version.
func (sr *SegmentReader) Version() int {
	if sr == nil || sr.meta == nil {
		return 0
	}
	return sr.meta.Version
}

// Wildcards returns embedded Z-list entries for segment v2 files.
// The returned slice is a view into the segment's backing memory (mmap or heap);
// it is valid only while the SegmentReader remains open and must not be modified.
func (sr *SegmentReader) Wildcards() core.Entries {
	if sr == nil || len(sr.wildcards) == 0 {
		return nil
	}
	return sr.wildcards
}

// Close releases the backing storage. For mmap-backed readers it unmaps the
// region; for byte-slice readers it is a no-op. Close is safe to call multiple
// times. Callers must ensure no in-flight query still holds posting iterators
// from this reader before calling Close, since unmapping invalidates the
// zero-copy views. Readers opened via OpenSegmentFile also register a finalizer
// that unmaps on GC, so forgetting to Close leaks the mapping until collection
// rather than crashing in-flight queries.
func (sr *SegmentReader) Close() error {
	if sr == nil || sr.closer == nil {
		return nil
	}
	closer := sr.closer
	sr.closer = nil
	runtime.SetFinalizer(sr, nil)
	return closer()
}
