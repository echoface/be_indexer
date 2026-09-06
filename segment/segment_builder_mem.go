package segment

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// InMemorySegmentBuilderOptions controls optional features for
// InMemorySegmentBuilder.
type InMemorySegmentBuilderOptions struct {
	SchemaHash string
	Wildcards  core.Entries
}

// InMemorySegmentBuilder builds a complete memory-mappable segment. All
// posting and indexing logic is delegated to per-field IndexBuilder
// instances; the framework is responsible only for matching IndexBuilder
// instances to fields, scheduling AddRecord/Build, and writing the segment
// binary format (MagicNumber, block alignment, MetaBlock, wildcards).
type InMemorySegmentBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	builders map[string]IndexBuilder

	w      io.Writer
	offset uint64

	schemaHash     string
	wildcards      core.Entries
	blockChecksums map[string]string
	curChecksum    hash.Hash
}

func NewInMemorySegmentBuilder(w io.Writer) *InMemorySegmentBuilder {
	return NewInMemorySegmentBuilderWithOptions(w, InMemorySegmentBuilderOptions{})
}

func NewInMemorySegmentBuilderWithOptions(w io.Writer, opts InMemorySegmentBuilderOptions) *InMemorySegmentBuilder {
	return &InMemorySegmentBuilder{
		fields:         make(map[string]FieldMetaDump),
		builders:       make(map[string]IndexBuilder),
		w:              w,
		schemaHash:     opts.SchemaHash,
		wildcards:      append(core.Entries(nil), opts.Wildcards...),
		blockChecksums: make(map[string]string),
	}
}

func (sw *InMemorySegmentBuilder) SetDocCount(count int) {
	sw.docCount = count
}

func (sw *InMemorySegmentBuilder) SetSchemaHash(schemaHash string) {
	sw.schemaHash = schemaHash
}

func (sw *InMemorySegmentBuilder) SetWildcards(entries core.Entries) {
	sw.wildcards = append(sw.wildcards[:0], entries...)
}

func (sw *InMemorySegmentBuilder) AddField(meta core.FieldMeta) error {
	name := string(meta.Field)
	sw.fields[name] = FieldMetaDump{
		Name:      name,
		ID:        meta.ID,
		IndexType: meta.IndexType,
	}
	indexName := meta.IndexType
	if indexName == "" {
		indexName = core.IndexNameDefault
	}
	env := BuilderEnv{MaxPostingsInMemory: 0, TmpDir: ""}
	cb, err := NewIndexBuilder(indexName, env)
	if err != nil {
		return fmt.Errorf("field %s: %w", name, err)
	}
	sw.builders[name] = cb
	return nil
}

// AddRecord delegates to the field's IndexBuilder.AddRecord.
func (sw *InMemorySegmentBuilder) AddRecord(field string, record any, entries []core.EntryID) error {
	cb, ok := sw.builders[field]
	if !ok {
		return fmt.Errorf("field %s not found", field)
	}
	return cb.AddRecord(record, entries)
}

// Write produces the final segment binary.
func (sw *InMemorySegmentBuilder) Write() error {
	if err := sw.writeBytes(MagicNumber); err != nil {
		return err
	}

	blockIndex := make(map[string]BlockDef)

	var sortedFields []string
	for f := range sw.fields {
		sortedFields = append(sortedFields, f)
	}
	sort.Strings(sortedFields)

	for _, field := range sortedFields {
		cb := sw.builders[field]
		bw := newSegmentBlockWriter(field, sw.w, &sw.offset, blockIndex, sw.blockChecksums)
		if err := cb.Build(bw); err != nil {
			return fmt.Errorf("field %s build failed: %w", field, err)
		}
	}

	wildcards := append(core.Entries(nil), sw.wildcards...)
	sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
	wildcardBytes := encodeEntriesBlock(wildcards)
	if err := sw.alignTo8(); err != nil {
		return err
	}
	wcOff := sw.offset
	sw.beginBlock()
	if err := sw.writeBytes(wildcardBytes); err != nil {
		return err
	}
	wcSum := sw.finishBlock(wildcardsBlockName)
	blockIndex[wildcardsBlockName] = BlockDef{
		Kind:     BlockKindWildcards,
		Offset:   wcOff,
		Size:     sw.offset - wcOff,
		Checksum: wcSum,
	}

	meta := MetaBlock{
		Version:        SegmentVersionV4,
		DocCount:       sw.docCount,
		SchemaHash:     sw.schemaHash,
		BlockIndex:     blockIndex,
		WildcardsBlock: wildcardsBlockName,
	}
	for _, field := range sortedFields {
		meta.Fields = append(meta.Fields, sw.fields[field])
	}

	// Fold checksums into BlockDef entries (segmentBlockWriter already registered
	// checksums directly; fold any remaining).
	for name, sum := range sw.blockChecksums {
		if def, ok := blockIndex[name]; ok {
			def.Checksum = sum
			blockIndex[name] = def
		}
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaOffset := sw.offset
	if err := sw.writeBytes(metaBytes); err != nil {
		return err
	}
	footer := make([]byte, 8)
	binary.LittleEndian.PutUint64(footer, metaOffset)
	return sw.writeBytes(footer)
}

func (sw *InMemorySegmentBuilder) writeBlock(b []byte) error {
	n, _ := sw.w.Write(b)
	sw.offset += uint64(n)
	if n != len(b) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(b))
	}
	return nil
}

func (sw *InMemorySegmentBuilder) alignTo8() error {
	pad := (8 - int(sw.offset%8)) % 8
	if pad == 0 {
		return nil
	}
	return sw.writeBytes(make([]byte, pad))
}

func (sw *InMemorySegmentBuilder) beginBlock() {
	sw.curChecksum = sha256.New()
}

func (sw *InMemorySegmentBuilder) finishBlock(name string) string {
	if sw.curChecksum == nil {
		return ""
	}
	sum := checksumPrefix + hex.EncodeToString(sw.curChecksum.Sum(nil))
	sw.curChecksum = nil
	sw.blockChecksums[name] = sum
	return sum
}

func (sw *InMemorySegmentBuilder) writeBytes(b []byte) error {
	n, err := sw.w.Write(b)
	sw.offset += uint64(n)
	if sw.curChecksum != nil && n > 0 {
		sw.curChecksum.Write(b[:n])
	}
	if err != nil {
		return err
	}
	if n != len(b) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(b))
	}
	return nil
}
