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
	// containers maps: Field -> ContainerBuilder for non-sortable containers
	containers map[string]ContainerBuilder
	// sortableFields tracks which fields have SortableBuilder containers
	sortableFields map[string]bool

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
		containers:     make(map[string]ContainerBuilder),
		sortableFields: make(map[string]bool),
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

func (sw *InMemorySegmentBuilder) ensureContainer(field string, cb ContainerBuilder) {
	if _, ok := sw.containers[field]; !ok {
		sw.containers[field] = cb
	}
}

// AddRecord routes a record and its EntryIDs to the appropriate container or Dict posting path.
func (sw *InMemorySegmentBuilder) AddRecord(field, container string, record any, entries []core.EntryID) error {
	if _, ok := sw.fields[field]; !ok {
		return fmt.Errorf("field %s not found", field)
	}

	cb, _ := NewContainerBuilder(container)
	if cb == nil {
		// No registered builder: Dict path via term postings
		term, ok := record.(string)
		if !ok {
			return fmt.Errorf("field %s: Dict posting requires string record, got %T", field, record)
		}
		fd := sw.ensureFieldData(field)
		fd.Postings[term] = append(fd.Postings[term], entries...)
		return nil
	}
	if _, ok := cb.(SortableBuilder); ok {
		sw.sortableFields[field] = true
		if term, ok := record.(string); ok {
			fd := sw.ensureFieldData(field)
			fd.Postings[term] = append(fd.Postings[term], entries...)
		}
		sw.ensureContainer(field, cb)
		return nil
	}
	// BatchBuilder or no-op container
	existing, has := sw.containers[field]
	if !has {
		sw.containers[field] = cb
		existing = cb
	}
	if bb, ok := existing.(BatchBuilder); ok {
		return bb.AddPosting(record, entries)
	}
	return nil
}

// AddPosting is deprecated; use AddRecord instead.
func (sw *InMemorySegmentBuilder) AddPosting(k int, field string, term string, entries []core.EntryID) error {
	container := core.IndexNameDefault
	if fd, ok := sw.fields[field]; ok && fd.Container != "" {
		container = fd.Container
	}
	return sw.AddRecord(field, container, term, entries)
}

func (sw *InMemorySegmentBuilder) Write() error {
	if err := sw.writeBytes(MagicNumber); err != nil {
		return err
	}

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

	for _, field := range sortedFields {
		meta := sw.fields[field]

		// Write Dict + Postings blocks for fields with fieldData
		if fd, ok := sw.fieldData[field]; ok {
			var terms []string
			for term := range fd.Postings {
				terms = append(terms, term)
			}
			sort.Strings(terms)

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

			dictBlockName := dictBlockName(field)
			dictOffset := sw.offset
			dictBytes := WriteFlatDict(fd.Dict)
			sw.beginBlock()
			if err := sw.writeBytes(dictBytes); err != nil {
				return err
			}
			sw.finishBlock(dictBlockName)
			blockIndex[dictBlockName] = BlockDef{Field: field, Kind: BlockKindDict, Offset: dictOffset, Size: sw.offset - dictOffset}

			// Feed SortableContainer with sorted (term, ref) pairs
			if sw.sortableFields[field] {
				if cb, ok := sw.containers[field]; ok {
					if sb, ok2 := cb.(SortableBuilder); ok2 {
						for _, term := range terms {
							ref := fd.Dict[term]
							entries := fd.Postings[term]
							if err := sb.AddKeyedPosting([]byte(term), ref, entries); err != nil {
								return fmt.Errorf("failed to add keyed posting to %s for field %s: %w", meta.Container, field, err)
							}
						}
					}
				}
			}
		}

		// Write container block for fields with registered containers
		if cb, ok := sw.containers[field]; ok {
			blockBytes, err := cb.Build()
			if err != nil {
				return fmt.Errorf("failed to build container %q for field %s: %w", meta.Container, field, err)
			}
			if len(blockBytes) > 0 {
				kind := meta.Container
				if kind == "" {
					kind = core.IndexNameDefault
				}
				containerBlockName := containerBlockName(field, kind)
				containerOffset := sw.offset
				sw.beginBlock()
				if err := sw.writeBytes(blockBytes); err != nil {
					return err
				}
				sw.finishBlock(containerBlockName)
				blockIndex[containerBlockName] = BlockDef{Field: field, Kind: kind, Offset: containerOffset, Size: sw.offset - containerOffset}
			}
		}
	}

	wildcards := append(core.Entries(nil), sw.wildcards...)
	sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
	wildcardBytes := encodeEntriesBlock(wildcards)
	if err := sw.alignTo8(); err != nil {
		return err
	}
	wildcardOffset := sw.offset
	sw.beginBlock()
	if err := sw.writeBytes(wildcardBytes); err != nil {
		return err
	}
	sw.finishBlock(wildcardsBlockName)
	blockIndex[wildcardsBlockName] = BlockDef{Kind: BlockKindWildcards, Offset: wildcardOffset, Size: sw.offset - wildcardOffset}

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

	metaOffset := sw.offset
	if err := sw.writeBytes(metaBytes); err != nil {
		return err
	}

	footer := make([]byte, 8)
	binary.LittleEndian.PutUint64(footer, metaOffset)
	return sw.writeBytes(footer)
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
