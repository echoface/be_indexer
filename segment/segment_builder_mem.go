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

// InMemorySegmentBuilderOptions controls optional segment format features for
// InMemorySegmentBuilder.
type InMemorySegmentBuilderOptions struct {
	SchemaHash string
	Wildcards  core.Entries
}

// FieldData represents the inverted index data for a single field before writing
type FieldData struct {
	Dict     map[string]PostingRef     // Term -> posting list ref (offset + count)
	Postings map[string][]core.EntryID // Term -> List of Entries
}

// InMemorySegmentBuilder builds a complete memory-mappable segment while
// materializing all postings in memory. It is intended for tests, small delta
// segments, and bounded-size chunks. Large full builds should use
// ExternalBuilder, which spills sorted runs to disk.
type InMemorySegmentBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	// fieldData maps: Field -> FieldData (all K values merged)
	fieldData map[string]*FieldData
	// rangeData maps: Field -> intervals for ext_range fields (all K merged).
	rangeData map[string][]Interval

	// Output
	w      io.Writer
	offset uint64

	schemaHash           string
	wildcards            core.Entries
	blockChecksums       map[string]string
	currentBlockChecksum hash.Hash
}

func NewInMemorySegmentBuilder(w io.Writer) *InMemorySegmentBuilder {
	return NewInMemorySegmentBuilderWithOptions(w, InMemorySegmentBuilderOptions{})
}

// NewInMemorySegmentBuilderWithOptions creates an in-memory segment builder
// with optional v2 metadata.
func NewInMemorySegmentBuilderWithOptions(w io.Writer, opts InMemorySegmentBuilderOptions) *InMemorySegmentBuilder {
	return &InMemorySegmentBuilder{
		fields:         make(map[string]FieldMetaDump),
		fieldData:      make(map[string]*FieldData),
		rangeData:      make(map[string][]Interval),
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

func (sw *InMemorySegmentBuilder) AddField(meta core.FieldMeta) {
	sw.fields[string(meta.Field)] = FieldMetaDump{
		Name:      string(meta.Field),
		ID:        meta.ID,
		Container: meta.Container,
		Parser:    meta.Tokenizer,
	}
}

func (sw *InMemorySegmentBuilder) ensureFieldData(field string) *FieldData {
	fd, ok := sw.fieldData[field]
	if !ok {
		fd = &FieldData{
			Dict:     make(map[string]PostingRef),
			Postings: make(map[string][]core.EntryID),
		}
		sw.fieldData[field] = fd
	}
	return fd
}

func (sw *InMemorySegmentBuilder) AddPosting(k int, field string, term string, entries []core.EntryID) error {
	if _, ok := sw.fields[field]; !ok {
		return fmt.Errorf("field %s not found", field)
	}
	fd := sw.ensureFieldData(field)
	fd.Postings[term] = append(fd.Postings[term], entries...)
	return nil
}

// AddRangePosting records a closed interval [lo, hi] -> entry mapping for an
// ext_range field. Intervals are materialized into a segment-tree block at Write.
func (sw *InMemorySegmentBuilder) AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error {
	if _, ok := sw.fields[field]; !ok {
		return fmt.Errorf("field %s not found", field)
	}
	sw.rangeData[field] = append(sw.rangeData[field], Interval{Lo: lo, Hi: hi, Entry: entry})
	return nil
}

func (sw *InMemorySegmentBuilder) Write() error {
	// 1. Write Magic Number
	if err := sw.writeBytes(MagicNumber); err != nil {
		return err
	}

	// Sort postings before writing
	for _, fd := range sw.fieldData {
		for _, entries := range fd.Postings {
			sort.Slice(entries, func(i, j int) bool {
				return entries[i] < entries[j]
			})
		}
	}

	blockIndex := make(map[string]BlockDef)

	var sortedFields []string
	for f := range sw.fields {
		sortedFields = append(sortedFields, f)
	}
	sort.Strings(sortedFields)

	// Write Postings and Dict for each field (all K merged)
	for _, field := range sortedFields {
		fd, ok := sw.fieldData[field]
		if !ok {
			continue
		}
		meta := sw.fields[field]

		// Sort terms to write postings deterministically
		var terms []string
		for term := range fd.Postings {
			terms = append(terms, term)
		}
		sort.Strings(terms)

		// A: Write Postings Block (all K merged, sorted by EntryID)
		postingsBlockName := plBlockName(field)
		if err := sw.alignTo8(); err != nil {
			return err
		}
		postingsOffset := sw.offset
		sw.beginBlock()
		for _, term := range terms {
			entries := fd.Postings[term]
			fd.Dict[term] = PostingRef{Offset: sw.offset - postingsOffset, Count: uint32(len(entries))}
			plBytes := WriteFlatPostingList(entries)
			if err := sw.writeBytes(plBytes); err != nil {
				return err
			}
		}
		postingsSize := sw.offset - postingsOffset
		sw.finishBlock(postingsBlockName)
		blockIndex[postingsBlockName] = BlockDef{Field: field, Kind: BlockKindPostings, Offset: postingsOffset, Size: postingsSize}

		if meta.Container != "" && meta.Container != core.IndexNameDefault {
			cb, err := NewContainerBuilder(meta.Container)
			switch {
			case err != nil:
				// container not registered or has no builder — skip
			case cb != nil:
				for _, term := range terms {
					cb.Add(term, fd.Dict[term])
				}
				blockBytes, err := cb.Build()
				if err != nil {
					return fmt.Errorf("failed to build container %q for field %s: %w", meta.Container, field, err)
				}
				containerBlockName := blockName(field) + "_" + meta.Container
				containerOffset := sw.offset
				sw.beginBlock()
				if err := sw.writeBytes(blockBytes); err != nil {
					return err
				}
				sw.finishBlock(containerBlockName)
				blockIndex[containerBlockName] = BlockDef{Field: field, Kind: meta.Container, Offset: containerOffset, Size: sw.offset - containerOffset}
			}
		}

		// B: Write Dictionary Block
		dictBlockName := dictBlockName(field)
		dictOffset := sw.offset
		dictBytes := WriteFlatDict(fd.Dict)
		sw.beginBlock()
		if err := sw.writeBytes(dictBytes); err != nil {
			return err
		}
		dictSize := sw.offset - dictOffset
		sw.finishBlock(dictBlockName)
		blockIndex[dictBlockName] = BlockDef{Field: field, Kind: BlockKindDict, Offset: dictOffset, Size: dictSize}
	}

	// Write range (segment-tree) blocks for ext_range fields (all K merged)
	var rfields []string
	for f := range sw.rangeData {
		rfields = append(rfields, f)
	}
	sort.Strings(rfields)
	for _, field := range rfields {
		intervals := sw.rangeData[field]
		rangeBytes, err := BuildRangeIndex(intervals)
		if err != nil {
			return fmt.Errorf("failed to build range index for field %s: %w", field, err)
		}
		name := containerBlockName(field, BlockKindRange)
		if err := sw.alignTo8(); err != nil {
			return err
		}
		rangeOffset := sw.offset
		sw.beginBlock()
		if err := sw.writeBytes(rangeBytes); err != nil {
			return err
		}
		sw.finishBlock(name)
		blockIndex[name] = BlockDef{Field: field, Kind: BlockKindRange, Offset: rangeOffset, Size: sw.offset - rangeOffset}
	}

	wildcards := append(core.Entries(nil), sw.wildcards...)
	sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
	wildcardBytes := encodeEntriesBlock(wildcards)
	wildcardOffset := sw.offset
	sw.beginBlock()
	if err := sw.writeBytes(wildcardBytes); err != nil {
		return err
	}
	sw.finishBlock(wildcardsBlockName)
	blockIndex[wildcardsBlockName] = BlockDef{Kind: BlockKindWildcards, Offset: wildcardOffset, Size: sw.offset - wildcardOffset}

	// 2. Prepare Metadata
	for name, sum := range sw.blockChecksums {
		if def, ok := blockIndex[name]; ok {
			def.Checksum = sum
			blockIndex[name] = def
		}
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

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	// 3. Write Metadata
	metaOffset := sw.offset
	if err := sw.writeBytes(metaBytes); err != nil {
		return err
	}

	// 4. Write Footer
	footer := make([]byte, 8)
	binary.LittleEndian.PutUint64(footer, metaOffset)
	if err := sw.writeBytes(footer); err != nil {
		return err
	}

	return nil
}

func (sw *InMemorySegmentBuilder) writeBytes(b []byte) error {
	n, err := sw.w.Write(b)
	sw.offset += uint64(n)
	if sw.currentBlockChecksum != nil && n > 0 {
		_, _ = sw.currentBlockChecksum.Write(b[:n])
	}
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

func (sw *InMemorySegmentBuilder) beginBlock() {
	sw.currentBlockChecksum = sha256.New()
}

// alignTo8 writes zero padding (outside any checksummed block) so that the next
// block starts on an 8-byte boundary. Padding bytes are not part of any block
// and are never read back; readers only index blocks via their recorded offset.
func (sw *InMemorySegmentBuilder) alignTo8() error {
	pad := (8 - int(sw.offset%8)) % 8
	if pad == 0 {
		return nil
	}
	return sw.writeBytes(make([]byte, pad))
}

func (sw *InMemorySegmentBuilder) finishBlock(name string) {
	if sw.currentBlockChecksum == nil {
		return
	}
	sw.blockChecksums[name] = checksumPrefix + hex.EncodeToString(sw.currentBlockChecksum.Sum(nil))
	sw.currentBlockChecksum = nil
}
