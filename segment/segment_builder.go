package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// DefaultMaxPostingsInMemory is the distinct-key spill threshold used by
// streaming callers that want the framework default. It is deliberately NOT
// applied implicitly by NewSegmentBuilder: MaxPostingsInMemory <= 0 means
// "disable spill", so a caller that wants bounded-memory streaming must resolve
// this default explicitly.
const DefaultMaxPostingsInMemory = 1_000_000

// SegmentBuilderOptions configures a SegmentBuilder. The zero value builds the
// entire segment in memory with no spill and no wildcards.
type SegmentBuilderOptions struct {
	// MaxPostingsInMemory is the distinct-key threshold at which a field
	// collector spills a sorted run to TmpDir. <= 0 disables spill entirely
	// (in-memory only). When > 0, TmpDir must be non-empty.
	MaxPostingsInMemory int
	// TmpDir is the directory for spill runs. Ignored when
	// MaxPostingsInMemory <= 0.
	TmpDir string
	// Wildcards are the K=0 entries encoded into the __wildcards block.
	Wildcards core.Entries
	// WildcardsBlockFile, when non-empty, is a pre-sorted sidecar in
	// encodeEntriesBlock format that is streamed into the __wildcards block
	// instead of Wildcards. It takes precedence over Wildcards. The file is
	// created by the caller after construction, so it is not validated here.
	WildcardsBlockFile string
}

// SegmentBuilder builds a complete memory-mappable segment. All posting and
// indexing logic is delegated to per-field IndexBuilder instances; the framework
// is responsible only for matching IndexBuilder instances to fields, scheduling
// AddRecord/Build, and writing the segment binary format (MagicNumber, block
// alignment, MetaBlock, wildcards).
//
// It is the single segment-construction path: with MaxPostingsInMemory <= 0 it
// behaves as a pure in-memory builder, and with a positive threshold plus TmpDir
// it spills per-field sorted runs to disk (external sort). Both profiles emit
// byte-identical segments.
type SegmentBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	builders map[string]IndexBuilder

	w      io.Writer
	offset uint64

	maxRecs            int
	tmpDir             string
	wildcards          core.Entries
	wildcardsBlockFile string

	blockChecksums map[string]string
	curChecksum    hash.Hash
}

// NewSegmentBuilder creates a segment builder. It fails only on an invalid
// option combination: spill enabled without a temp directory.
func NewSegmentBuilder(w io.Writer, opts SegmentBuilderOptions) (*SegmentBuilder, error) {
	if opts.MaxPostingsInMemory > 0 && opts.TmpDir == "" {
		return nil, fmt.Errorf("segment builder: TmpDir is required when MaxPostingsInMemory > 0")
	}
	return &SegmentBuilder{
		fields:             make(map[string]FieldMetaDump),
		builders:           make(map[string]IndexBuilder),
		w:                  w,
		maxRecs:            opts.MaxPostingsInMemory,
		tmpDir:             opts.TmpDir,
		wildcards:          append(core.Entries(nil), opts.Wildcards...),
		wildcardsBlockFile: opts.WildcardsBlockFile,
		blockChecksums:     make(map[string]string),
	}, nil
}

// NewInMemorySegmentBuilder creates the zero-spill convenience builder. It
// cannot fail because spill is disabled, so TmpDir is never required.
func NewInMemorySegmentBuilder(w io.Writer) *SegmentBuilder {
	b, _ := NewSegmentBuilder(w, SegmentBuilderOptions{})
	return b
}

func (b *SegmentBuilder) SetDocCount(count int) {
	b.docCount = count
}

// SetWildcards sets in-memory wildcard entries. Any previously configured
// wildcard sidecar is cleared: the last source set wins.
func (b *SegmentBuilder) SetWildcards(entries core.Entries) {
	b.wildcards = append(b.wildcards[:0], entries...)
	b.wildcardsBlockFile = ""
}

// SetWildcardsBlockFile streams the wildcard block from a pre-sorted sidecar
// instead of in-memory entries. Any in-memory wildcard entries are cleared: the
// last source set wins.
func (b *SegmentBuilder) SetWildcardsBlockFile(path string) {
	b.wildcardsBlockFile = path
	b.wildcards = nil
}

func (b *SegmentBuilder) AddField(field core.BEField, option core.FieldOption) error {
	normalized, err := core.NormalizeFieldOption(field, option)
	if err != nil {
		return err
	}
	name := string(field)
	if _, exists := b.fields[name]; exists {
		return fmt.Errorf("field %s already added", name)
	}
	b.fields[name] = FieldMetaDump{
		Name:      name,
		IndexType: normalized.IndexType,
		Encoder:   normalized.Encoder,
	}
	indexName := normalized.IndexType
	env := BuilderEnv{
		MaxPostingsInMemory: b.maxRecs,
		TmpDir:              b.tmpDir,
	}
	cb, err := NewIndexBuilder(indexName, env)
	if err != nil {
		return fmt.Errorf("field %s: %w", name, err)
	}
	b.builders[name] = cb
	return nil
}

// AddRecord delegates to the field's IndexBuilder.AddRecord.
func (b *SegmentBuilder) AddRecord(field string, record any, entries []core.EntryID) error {
	cb, ok := b.builders[field]
	if !ok {
		return fmt.Errorf("field %s not found", field)
	}
	return cb.AddRecord(record, entries)
}

// Write produces the final segment binary. Spill runs (when any) are removed
// afterwards, whether Write succeeds or fails.
func (b *SegmentBuilder) Write() error {
	defer b.cleanupRuns()
	if err := b.writeBytes(MagicNumber); err != nil {
		return err
	}

	blockIndex := make(map[string]BlockDef)

	fields := make([]string, 0, len(b.fields))
	for f := range b.fields {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	for _, field := range fields {
		cb := b.builders[field]
		bw := newSegmentBlockWriter(field, b.w, &b.offset, blockIndex, b.blockChecksums)
		if err := cb.Build(bw); err != nil {
			return fmt.Errorf("field %s build failed: %w", field, err)
		}
	}

	if err := b.writeWildcardsBlock(blockIndex); err != nil {
		return err
	}

	meta := MetaBlock{
		Version:        SegmentVersionV5,
		DocCount:       b.docCount,
		BlockIndex:     blockIndex,
		WildcardsBlock: wildcardsBlockName,
	}
	for _, field := range fields {
		meta.Fields = append(meta.Fields, b.fields[field])
	}
	schemaHash, err := schemaHashFromFieldDumps(meta.Fields)
	if err != nil {
		return err
	}
	meta.SchemaHash = schemaHash

	// Fold field-block checksums registered by segmentBlockWriter into their
	// BlockDef entries. The wildcards BlockDef already carries its checksum.
	for name, sum := range b.blockChecksums {
		if def, ok := blockIndex[name]; ok {
			def.Checksum = sum
			blockIndex[name] = def
		}
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaOffset := b.offset
	if err := b.writeBytes(metaBytes); err != nil {
		return err
	}
	footer := make([]byte, 8)
	binary.LittleEndian.PutUint64(footer, metaOffset)
	return b.writeBytes(footer)
}

// writeWildcardsBlock writes the __wildcards block from either the in-memory
// entries (sorted here) or a pre-sorted sidecar streamed verbatim. Both sources
// produce byte-identical blocks: the sidecar format equals encodeEntriesBlock.
func (b *SegmentBuilder) writeWildcardsBlock(blockIndex map[string]BlockDef) error {
	if err := b.alignTo8(); err != nil {
		return err
	}
	wcOff := b.offset
	b.beginBlock()
	if b.wildcardsBlockFile != "" {
		if err := b.copyFromFile(b.wildcardsBlockFile); err != nil {
			return err
		}
	} else {
		wildcards := append(core.Entries(nil), b.wildcards...)
		sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
		if err := b.writeBytes(encodeEntriesBlock(wildcards)); err != nil {
			return err
		}
	}
	wcSum := b.finishBlock()
	blockIndex[wildcardsBlockName] = BlockDef{
		Kind:     BlockKindWildcards,
		Offset:   wcOff,
		Size:     b.offset - wcOff,
		Checksum: wcSum,
	}
	return nil
}

// copyFromFile streams the wildcard sidecar into the segment in bounded chunks,
// keeping the running offset and block checksum in sync exactly as writeBytes
// does for an in-memory slice. The whole sidecar is never materialized.
func (b *SegmentBuilder) copyFromFile(path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if werr := b.writeBytes(buf[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (b *SegmentBuilder) writeBytes(data []byte) error {
	n, err := b.w.Write(data)
	b.offset += uint64(n)
	if b.curChecksum != nil && n > 0 {
		b.curChecksum.Write(data[:n])
	}
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(data))
	}
	return nil
}

func (b *SegmentBuilder) alignTo8() error {
	pad := (8 - int(b.offset%8)) % 8
	if pad == 0 {
		return nil
	}
	return b.writeBytes(make([]byte, pad))
}

func (b *SegmentBuilder) beginBlock() {
	b.curChecksum = sha256.New()
}

func (b *SegmentBuilder) finishBlock() string {
	if b.curChecksum == nil {
		return ""
	}
	sum := checksumPrefix + hex.EncodeToString(b.curChecksum.Sum(nil))
	b.curChecksum = nil
	b.blockChecksums[wildcardsBlockName] = sum
	return sum
}

// cleanupRuns removes the spill directory after a build. The guard keeps an
// in-memory build (tmpDir == "") from ever touching the filesystem.
func (b *SegmentBuilder) cleanupRuns() {
	if b.tmpDir != "" {
		_ = os.RemoveAll(b.tmpDir)
	}
}
