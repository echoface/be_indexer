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

const (
	defaultMaxPostingsInMemory = 1_000_000
)

// ExternalBuilderOptions controls bounded-memory segment construction.
type ExternalBuilderOptions struct {
	MaxPostingsInMemory int
	SchemaHash          string
	Wildcards           core.Entries
	WildcardsBlockFile  string
}

// ExternalBuilder builds the same mmap segment format as InMemorySegmentBuilder.
// Posting and indexing logic is delegated to per-field IndexBuilder instances;
// spill/merge is handled by each building#s KeyedPostingCollector via BuilderEnv.
type ExternalBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	builders map[string]IndexBuilder
	w        io.Writer
	tmpDir   string
	offset   uint64
	maxRecs  int

	schemaHash         string
	wildcards          core.Entries
	wildcardsBlockFile string
	blockChecksums     map[string]string
	curChecksum        hash.Hash
}

func NewExternalBuilder(w io.Writer, tmpDir string, opts ExternalBuilderOptions) *ExternalBuilder {
	maxRecs := opts.MaxPostingsInMemory
	if maxRecs <= 0 {
		maxRecs = defaultMaxPostingsInMemory
	}
	return &ExternalBuilder{
		fields:             make(map[string]FieldMetaDump),
		builders:           make(map[string]IndexBuilder),
		w:                  w,
		tmpDir:             tmpDir,
		maxRecs:            maxRecs,
		schemaHash:         opts.SchemaHash,
		wildcards:          append(core.Entries(nil), opts.Wildcards...),
		wildcardsBlockFile: opts.WildcardsBlockFile,
		blockChecksums:     make(map[string]string),
	}
}

func (b *ExternalBuilder) SetDocCount(count int) {
	b.docCount = count
}

func (b *ExternalBuilder) SetSchemaHash(schemaHash string) {
	b.schemaHash = schemaHash
}

func (b *ExternalBuilder) SetWildcards(entries core.Entries) {
	b.wildcards = append(b.wildcards[:0], entries...)
	b.wildcardsBlockFile = ""
}

func (b *ExternalBuilder) SetWildcardsBlockFile(path string) {
	b.wildcardsBlockFile = path
	b.wildcards = nil
}

func (b *ExternalBuilder) AddField(meta core.FieldMeta) error {
	name := string(meta.Field)
	b.fields[name] = FieldMetaDump{
		Name:      name,
		ID:        meta.ID,
		IndexType: meta.IndexType,
	}
	indexName := meta.IndexType
	if indexName == "" {
		indexName = core.IndexNameDefault
	}
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

func (b *ExternalBuilder) AddRecord(field string, record any, entries []core.EntryID) error {
	cb, ok := b.builders[field]
	if !ok {
		return fmt.Errorf("field %s not found", field)
	}
	return cb.AddRecord(record, entries)
}

func (b *ExternalBuilder) AddPosting(k int, field string, term string, entries []core.EntryID) error {
	return b.AddRecord(field, term, entries)
}

func (b *ExternalBuilder) Write() error {
	defer b.cleanupRuns()
	if err := b.writeBytes(MagicNumber); err != nil {
		return err
	}

	blockIndex := make(map[string]BlockDef)

	var sortedFields []string
	for f := range b.fields {
		sortedFields = append(sortedFields, f)
	}
	sort.Strings(sortedFields)

	for _, field := range sortedFields {
		cb := b.builders[field]
		bw := newSegmentBlockWriter(field, b.w, &b.offset, blockIndex, b.blockChecksums)
		if err := cb.Build(bw); err != nil {
			return fmt.Errorf("field %s build failed: %w", field, err)
		}
	}

	if b.wildcardsBlockFile != "" {
		f, err := os.Open(filepath.Clean(b.wildcardsBlockFile))
		if err != nil {
			return err
		}
		// Stream the sidecar straight into the segment instead of io.ReadAll'ing
		// it into memory first. The sidecar's on-disk format (BEIENT1 magic +
		// count + 8-byte entries) is byte-identical to encodeEntriesBlock, so a
		// verbatim copy yields the same wildcard block.
		err = b.writeWildcardsFromReader(blockIndex, f)
		f.Close()
		if err != nil {
			return err
		}
	} else {
		wildcards := append(core.Entries(nil), b.wildcards...)
		sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
		b.writeWildcards(blockIndex, encodeEntriesBlock(wildcards))
	}

	meta := MetaBlock{
		Version:        SegmentVersionV4,
		DocCount:       b.docCount,
		SchemaHash:     b.schemaHash,
		BlockIndex:     blockIndex,
		WildcardsBlock: wildcardsBlockName,
	}
	for _, field := range sortedFields {
		meta.Fields = append(meta.Fields, b.fields[field])
	}

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

func (b *ExternalBuilder) writeWildcards(blockIndex map[string]BlockDef, data []byte) error {
	b.alignTo8()
	wcOff := b.offset
	b.beginBlock(wildcardsBlockName)
	if err := b.writeBytes(data); err != nil {
		return err
	}
	b.finishBlock()
	blockIndex[wildcardsBlockName] = BlockDef{
		Kind: BlockKindWildcards,
		Offset: wcOff,
		Size: b.offset - wcOff,
	}
	return nil
}

// writeWildcardsFromReader streams the wildcard block from r straight into the
// segment, computing the block checksum on the fly. It never materializes the
// whole sidecar in memory, so the wildcard-spill memory bound is preserved
// through to segment write. Byte-for-byte equivalent to buffering r fully and
// calling writeWildcards.
func (b *ExternalBuilder) writeWildcardsFromReader(blockIndex map[string]BlockDef, r io.Reader) error {
	b.alignTo8()
	wcOff := b.offset
	b.beginBlock(wildcardsBlockName)
	if err := b.copyFrom(r); err != nil {
		return err
	}
	b.finishBlock()
	blockIndex[wildcardsBlockName] = BlockDef{
		Kind:   BlockKindWildcards,
		Offset: wcOff,
		Size:   b.offset - wcOff,
	}
	return nil
}

// copyFrom streams r into the segment writer in bounded chunks, keeping the
// running offset and block checksum in sync exactly as writeBytes does for an
// in-memory slice.
func (b *ExternalBuilder) copyFrom(r io.Reader) error {
	buf := make([]byte, 64*1024)
	for {
		n, err := r.Read(buf)
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

func (b *ExternalBuilder) writeBytes(data []byte) error {
	n, err := b.w.Write(data)
	b.offset += uint64(n)
	if b.curChecksum != nil && n > 0 {
		b.curChecksum.Write(data[:n])
	}
	if err != nil {
		return err
	}
	if n != len(data) {
		return fmt.Errorf("short write")
	}
	return nil
}

func (b *ExternalBuilder) alignTo8() error {
	pad := (8 - int(b.offset%8)) % 8
	if pad == 0 {
		return nil
	}
	return b.writeBytes(make([]byte, pad))
}

func (b *ExternalBuilder) beginBlock(name string) {
	b.curChecksum = sha256.New()
}

func (b *ExternalBuilder) finishBlock() {
	if b.curChecksum == nil {
		return
	}
	if name := wildcardsBlockName; name != "" {
		b.blockChecksums[name] = checksumPrefix + hex.EncodeToString(b.curChecksum.Sum(nil))
	}
	b.curChecksum = nil
}

func (b *ExternalBuilder) cleanupRuns() {
	if b.tmpDir != "" {
		os.RemoveAll(b.tmpDir)
	}
}
