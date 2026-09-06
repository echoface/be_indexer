package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"

	mmapgo "github.com/blevesearch/mmap-go"

	"github.com/echoface/be_indexer/core"
)

// blockLookup holds pre-resolved references for a field (all K values merged).
type blockLookup struct {
	dict    *FlatDict
	pl      []byte
	readers map[string]IndexReader
}

// ResolvedField is an immutable, pre-resolved view of one field in a segment.
// BooleanEngine resolves these views once at construction time, so retrieval
// does not repeat field-name map lookups for every encoded query value.
type ResolvedField struct {
	name  core.BEField
	block *blockLookup
}

// MatchQuery dispatches a physical query to this field's registered index.
func (f *ResolvedField) MatchQuery(kind string, query interface{}) ([]core.PostingIterator, error) {
	if f == nil || f.block == nil || f.block.readers == nil {
		return nil, nil
	}
	reader, ok := f.block.readers[kind]
	if !ok {
		return nil, nil
	}
	return reader.MatchQuery(BlockContext{Pl: f.block.pl}, f.name, query)
}

type BlockChecksumMode int

const (
	// BlockChecksumOnOpen verifies every block while opening the reader. This is
	// the strictest mode and remains the default for direct NewSegmentReader calls and
	// tests that validate corruption handling.
	BlockChecksumOnOpen BlockChecksumMode = iota
	// BlockChecksumDisabled validates checksum metadata but skips the
	// O(segment_size) payload scan during reader construction. The caller must
	// explicitly establish trust in the artifact when choosing this mode.
	BlockChecksumDisabled
)

// ReaderOptions controls validation performed when constructing a segment reader.
type ReaderOptions struct {
	BlockChecksumMode BlockChecksumMode
}

// OpenFileOptions controls validation performed against the exact file handle
// that is subsequently memory-mapped, avoiding a path-level TOCTOU window.
type OpenFileOptions struct {
	ReaderOptions
	ExpectedSize     uint64
	ExpectedChecksum string
	VerifyFile       bool
}

// SegmentReader serves queries from an immutable segment byte slice. The byte slice
// may be backed by OS mmap or ordinary memory; all posting-list lookups are
// zero-copy views into that backing slice when alignment permits.
type SegmentReader struct {
	b         []byte
	meta      *MetaBlock
	fields    map[core.BEField]*ResolvedField
	wildcards core.Entries
	// closer releases the backing storage (e.g. munmap). It is nil for readers
	// over caller-owned byte slices, whose memory is reclaimed by the GC.
	closer func() error
}

// NewSegmentReader parses an immutable segment byte slice using strict block
// checksum validation. Use NewSegmentReaderWithOptions for serving paths that have
// already established artifact trust and explicitly want to skip the block scan.
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
	if meta.Version != SegmentVersionV5 {
		return nil, fmt.Errorf("unsupported segment version %d (expected v5)", meta.Version)
	}
	fields := make(map[core.BEField]*ResolvedField, len(meta.Fields))
	var wildcards core.Entries
	// Validate byte ranges before checksum metadata so malformed offsets and
	// sizes retain their direct, deterministic diagnostics.
	for name, def := range meta.BlockIndex {
		if _, err := checkedBlockBytes(b, metaOffset, def); err != nil {
			return nil, fmt.Errorf("invalid block %s: %w", name, err)
		}
	}
	if opts.BlockChecksumMode == BlockChecksumOnOpen {
		if err := verifyBlockChecksums(b, metaOffset, meta.BlockIndex); err != nil {
			return nil, err
		}
	} else if err := verifyBlockChecksumMetadata(meta.BlockIndex); err != nil {
		return nil, err
	}
	expectedSchemaHash, err := schemaHashFromFieldDumps(meta.Fields)
	if err != nil {
		return nil, fmt.Errorf("invalid segment schema: %w", err)
	}
	if meta.SchemaHash != expectedSchemaHash {
		return nil, fmt.Errorf("segment schema hash mismatch: got %s, computed %s", meta.SchemaHash, expectedSchemaHash)
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

	for _, fieldMeta := range meta.Fields {
		name := core.BEField(fieldMeta.Name)
		if name == "" {
			return nil, fmt.Errorf("segment declares an empty field name")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("segment declares duplicate field %q", name)
		}
		fields[name] = &ResolvedField{name: name, block: &blockLookup{}}
	}

	// Single O(blocks) pass: structured BlockDef carries (Field, kind).
	for name, def := range meta.BlockIndex {
		if def.Kind == BlockKindWildcards {
			continue // already decoded above
		}
		resolved, ok := fields[core.BEField(def.Field)]
		if !ok {
			return nil, fmt.Errorf("block %s references unknown field %q", name, def.Field)
		}
		blk := resolved.block

		blockBytes, err := checkedBlockBytes(b, metaOffset, def)
		if err != nil {
			return nil, fmt.Errorf("invalid block %s: %w", name, err)
		}
		switch def.Kind {
		case BlockKindDict:
			if blk.dict != nil {
				return nil, fmt.Errorf("field %s has duplicate %s block", def.Field, def.Kind)
			}
			dict, err := NewFlatDict(blockBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse dict for %s: %w", name, err)
			}
			blk.dict = dict
		case BlockKindPostings:
			if blk.pl != nil {
				return nil, fmt.Errorf("field %s has duplicate %s block", def.Field, def.Kind)
			}
			blk.pl = blockBytes
		default:
			cr, err := NewIndexReader(def.Kind, blockBytes)
			if err != nil {
				return nil, fmt.Errorf("failed to load container %q for %s: %w", def.Kind, name, err)
			}
			if blk.readers == nil {
				blk.readers = make(map[string]IndexReader)
			}
			if _, exists := blk.readers[def.Kind]; exists {
				return nil, fmt.Errorf("field %s has duplicate %s block", def.Field, def.Kind)
			}
			blk.readers[def.Kind] = cr
		}
	}

	// Ensure fields with a dict get a DictIndex with the loaded dict
	// so IndexQuery("default", ...) works for the default path.
	for _, field := range fields {
		blk := field.block
		if blk.dict != nil {
			if blk.readers == nil {
				blk.readers = make(map[string]IndexReader)
			}
			if _, ok := blk.readers[core.IndexNameDefault]; !ok {
				blk.readers[core.IndexNameDefault] = NewDictReader(blk.dict)
			}
		}
	}

	return &SegmentReader{
		b:         b,
		meta:      &meta,
		fields:    fields,
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
// toward RSS. File verification and mmap use the same file handle.
func OpenSegmentFile(path string, opts OpenFileOptions) (*SegmentReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if opts.ExpectedSize != 0 && uint64(info.Size()) != opts.ExpectedSize {
		return nil, fmt.Errorf("%s: size mismatch: got %d, want %d", path, info.Size(), opts.ExpectedSize)
	}
	if opts.VerifyFile {
		if opts.ExpectedChecksum == "" {
			return nil, fmt.Errorf("%s: expected checksum is required", path)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return nil, err
		}
		got := checksumPrefix + hex.EncodeToString(h.Sum(nil))
		if got != opts.ExpectedChecksum {
			return nil, fmt.Errorf("%s: checksum mismatch", path)
		}
	}

	data, err := mmapgo.Map(f, mmapgo.RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("mmap %s: %w", path, err)
	}

	reader, err := NewSegmentReaderWithOptions(data, opts.ReaderOptions)
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
		if len(def.Checksum) != len(checksumPrefix)+sha256.Size*2 || def.Checksum[:len(checksumPrefix)] != checksumPrefix {
			return fmt.Errorf("block %s checksum must use sha256 format", name)
		}
		decoded, err := hex.DecodeString(def.Checksum[len(checksumPrefix):])
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("block %s checksum must use sha256 format", name)
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

// ResolveField returns an immutable field handle. Callers on a hot path should
// resolve once and reuse the handle for all physical query values.
func (sr *SegmentReader) ResolveField(field core.BEField) (*ResolvedField, bool) {
	if sr == nil {
		return nil, false
	}
	resolved, ok := sr.fields[field]
	return resolved, ok
}

// GetPostingsByTerm returns a posting iterator for a physical term in the
// merged (all-K) posting list. This is a convenience wrapper around
// IndexQuery with the default dict container.
func (sr *SegmentReader) GetPostingsByTerm(field core.BEField, term string) (core.PostingIterator, error) {
	iters, err := sr.IndexQuery(field, core.IndexNameDefault, term)
	if err != nil {
		return nil, err
	}
	if len(iters) == 0 {
		return nil, nil
	}
	return iters[0], nil
}

// IndexQuery dispatches a query to a registered container and returns
// posting iterators. kind is the container type (e.g. "default",
// "ac_matcher", "ext_range").
func (sr *SegmentReader) IndexQuery(field core.BEField, kind string, query interface{}) ([]core.PostingIterator, error) {
	resolved, ok := sr.ResolveField(field)
	if !ok {
		return nil, core.ErrUnknownQueryField
	}
	return resolved.MatchQuery(kind, query)
}

// MultiPatternSearch performs AC automaton matching on the input text and
// returns posting iterators for all matched terms in the merged posting list.
func (sr *SegmentReader) MultiPatternSearch(field core.BEField, text string) ([]core.PostingIterator, error) {
	return sr.IndexQuery(field, BlockKindAC, text)
}

// GetRangePostings returns posting iterators for every interval that contains
// the query point in an ext_range field, from the merged (all-K) posting list.
func (sr *SegmentReader) GetRangePostings(field core.BEField, point int64) ([]core.PostingIterator, error) {
	return sr.IndexQuery(field, BlockKindRange, point)
}

// SchemaHash returns the embedded canonical schema hash for segment v5 files.
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

// Wildcards returns embedded Z-list entries for Segment v5 files.
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
