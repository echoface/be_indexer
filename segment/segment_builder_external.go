package segment

import (
	"bufio"
	"container/heap"
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
	maxOpenRunReaders          = 64
)

// ExternalBuilderOptions controls bounded-memory segment construction.
type ExternalBuilderOptions struct {
	// MaxPostingsInMemory controls how many flattened posting records may be held
	// before spilling a sorted run to disk. <= 0 uses a conservative default.
	MaxPostingsInMemory int
	SchemaHash          string
	Wildcards           core.Entries
	WildcardsBlockFile  string
}

// ExternalBuilder builds the same mmap segment format as Builder, but spills
// sorted posting runs to disk and performs a k-way merge during Write(). It is
// intended for full builds with millions of documents where materializing all
// postings in memory is not acceptable.
type ExternalBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	w        io.Writer
	tmpDir   string
	offset   uint64
	maxRecs  int

	records  []postingRecord
	runFiles []string

	rangeData map[string][]Interval

	// field <-> dense uint16 id used only inside on-disk run records to avoid
	// repeating the full field name on every posting record.
	fieldID   map[string]uint16
	fieldByID []string

	schemaHash         string
	wildcards          core.Entries
	wildcardsBlockFile string
	blockChecksums     map[string]string
	currentBlockName   string
	currentBlockHash   hash.Hash
}

type postingRecord struct {
	Field string
	Term  string
	Entry core.EntryID
}

// NewExternalBuilder creates an external-sort segment builder.
func NewExternalBuilder(w io.Writer, tmpDir string, opts ExternalBuilderOptions) *ExternalBuilder {
	maxRecs := opts.MaxPostingsInMemory
	if maxRecs <= 0 {
		maxRecs = defaultMaxPostingsInMemory
	}
	return &ExternalBuilder{
		fields:             make(map[string]FieldMetaDump),
		w:                  w,
		tmpDir:             tmpDir,
		maxRecs:            maxRecs,
		records:            make([]postingRecord, 0, maxRecs),
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

func (b *ExternalBuilder) AddField(meta core.FieldMeta) {
	name := string(meta.Field)
	b.fields[name] = FieldMetaDump{
		Name:      name,
		ID:        meta.ID,
		Container: meta.Container,
		Parser:    meta.Tokenizer,
	}
	if b.fieldID == nil {
		b.fieldID = make(map[string]uint16)
	}
	if _, ok := b.fieldID[name]; !ok {
		b.fieldID[name] = uint16(len(b.fieldByID))
		b.fieldByID = append(b.fieldByID, name)
	}
}

func (b *ExternalBuilder) AddPosting(k int, field string, term string, entries []core.EntryID) error {
	if _, ok := b.fields[field]; !ok {
		return fmt.Errorf("field %s not found", field)
	}
	for _, entry := range entries {
		b.records = append(b.records, postingRecord{Field: field, Term: term, Entry: entry})
		if len(b.records) >= b.maxRecs {
			if err := b.flushRun(); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddRangePosting records a closed interval for an ext_range field. Range
// intervals are kept in memory (their count is bounded by the number of range
// predicates, far smaller than the flattened EQ posting stream) and serialized
// into a segment-tree block at Write.
func (b *ExternalBuilder) AddRangePosting(k int, field string, lo, hi int64, entry core.EntryID) error {
	if _, ok := b.fields[field]; !ok {
		return fmt.Errorf("field %s not found", field)
	}
	if b.rangeData == nil {
		b.rangeData = make(map[string][]Interval)
	}
	b.rangeData[field] = append(b.rangeData[field], Interval{Lo: lo, Hi: hi, Entry: entry})
	return nil
}

func (b *ExternalBuilder) Write() error {
	defer b.cleanupRuns()
	if err := b.flushRun(); err != nil {
		return err
	}
	if err := b.compactRuns(); err != nil {
		return err
	}
	if err := b.writeBytes(MagicNumber); err != nil {
		return err
	}

	blockIndex, err := b.writeMergedBlocks()
	if err != nil {
		return err
	}

	if err := b.writeRangeBlocks(blockIndex); err != nil {
		return err
	}

	wildcardOffset := b.offset
	if b.wildcardsBlockFile != "" {
		if err := b.writeChecksummedFileBlock(wildcardsBlockName, b.wildcardsBlockFile); err != nil {
			return err
		}
	} else {
		wildcards := append(core.Entries(nil), b.wildcards...)
		sort.Slice(wildcards, func(i, j int) bool { return wildcards[i] < wildcards[j] })
		wildcardBytes := encodeEntriesBlock(wildcards)
		if err := b.writeChecksummedBlock(wildcardsBlockName, wildcardBytes); err != nil {
			return err
		}
	}
	blockIndex[wildcardsBlockName] = BlockDef{Kind: BlockKindWildcards, Offset: wildcardOffset, Size: b.offset - wildcardOffset}

	// Fold each block's checksum into its BlockDef.
	for name, sum := range b.blockChecksums {
		if def, ok := blockIndex[name]; ok {
			def.Checksum = sum
			blockIndex[name] = def
		}
	}
	meta := MetaBlock{
		Version:        SegmentVersionV4,
		DocCount:       b.docCount,
		SchemaHash:     b.schemaHash,
		Fields:         b.sortedFieldMeta(),
		BlockIndex:     blockIndex,
		WildcardsBlock: wildcardsBlockName,
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

func (b *ExternalBuilder) flushRun() error {
	if len(b.records) == 0 {
		return nil
	}
	sort.Slice(b.records, func(i, j int) bool { return lessPostingRecord(b.records[i], b.records[j]) })
	if err := os.MkdirAll(b.tmpDir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(b.tmpDir, "posting-run-*.bin")
	if err != nil {
		return err
	}
	name := f.Name()
	bw := bufio.NewWriterSize(f, 64*1024)
	for _, rec := range b.records {
		if err := b.writeRunRecord(bw, rec); err != nil {
			_ = f.Close()
			_ = os.Remove(name)
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	b.runFiles = append(b.runFiles, name)
	b.records = b.records[:0]
	return nil
}

func (b *ExternalBuilder) compactRuns() error {
	for len(b.runFiles) > maxOpenRunReaders {
		var next []string
		for start := 0; start < len(b.runFiles); start += maxOpenRunReaders {
			end := start + maxOpenRunReaders
			if end > len(b.runFiles) {
				end = len(b.runFiles)
			}
			batch := b.runFiles[start:end]
			merged, err := b.mergeRunBatch(batch)
			if err != nil {
				return err
			}
			next = append(next, merged)
			for _, file := range batch {
				_ = os.Remove(file)
			}
		}
		b.runFiles = next
	}
	return nil
}

func (b *ExternalBuilder) mergeRunBatch(files []string) (string, error) {
	out, err := os.CreateTemp(b.tmpDir, "posting-run-merged-*.bin")
	if err != nil {
		return "", err
	}
	outName := out.Name()
	readers := make([]*postingRunReader, 0, len(files))
	defer func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}()
	for _, file := range files {
		r, err := b.openPostingRunReader(file)
		if err != nil {
			_ = out.Close()
			_ = os.Remove(outName)
			return "", err
		}
		readers = append(readers, r)
	}
	if err := b.writeMergedRunRecords(out, readers); err != nil {
		_ = out.Close()
		_ = os.Remove(outName)
		return "", err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(outName)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outName)
		return "", err
	}
	return outName, nil
}

func (b *ExternalBuilder) writeMergedBlocks() (map[string]BlockDef, error) {
	blockIndex := make(map[string]BlockDef)
	if len(b.runFiles) == 0 {
		return blockIndex, nil
	}

	readers := make([]*postingRunReader, 0, len(b.runFiles))
	defer func() {
		for _, r := range readers {
			_ = r.Close()
		}
	}()
	for _, file := range b.runFiles {
		r, err := b.openPostingRunReader(file)
		if err != nil {
			return nil, err
		}
		readers = append(readers, r)
	}

	h := &postingRecordHeap{}
	heap.Init(h)
	for i, r := range readers {
		if ok, err := r.Next(); err != nil {
			return nil, err
		} else if ok {
			heap.Push(h, postingHeapItem{rec: r.Record(), reader: i})
		}
	}

	var currentField string
	var postingsOffset uint64
	var dict streamingDict
	groupOpen := false

	finishGroup := func() error {
		if !groupOpen {
			return nil
		}
		postingsSize := b.offset - postingsOffset
		postingsBlockName := plBlockName(currentField)
		b.finishBlock(postingsBlockName)
		blockIndex[postingsBlockName] = BlockDef{Field: currentField, Kind: BlockKindPostings, Offset: postingsOffset, Size: postingsSize}

		meta := b.fields[currentField]
		if cb, err := NewContainerBuilder(meta.Container); err == nil && cb != nil {
			for _, item := range dict.items {
				term := string(dict.terms[item.termStart : item.termStart+item.termLen])
				cb.Add(term, PostingRef{Offset: item.postingOff, Count: item.postingCount})
			}
			blockBytes, err := cb.Build()
			if err != nil {
				return fmt.Errorf("failed to build container %q for field %s: %w", meta.Container, currentField, err)
			}
			containerBlockName := blockName(currentField) + "_" + meta.Container
			containerOffset := b.offset
			if err := b.writeChecksummedBlock(containerBlockName, blockBytes); err != nil {
				return err
			}
			blockIndex[containerBlockName] = BlockDef{Field: currentField, Kind: meta.Container, Offset: containerOffset, Size: b.offset - containerOffset}
		}

		dictOffset := b.offset
		dictBytes := dict.Bytes()
		if err := b.writeChecksummedBlock(dictBlockName(currentField), dictBytes); err != nil {
			return err
		}
		blockIndex[dictBlockName(currentField)] = BlockDef{Field: currentField, Kind: BlockKindDict, Offset: dictOffset, Size: b.offset - dictOffset}
		groupOpen = false
		return nil
	}

	for h.Len() > 0 {
		first := heap.Pop(h).(postingHeapItem)
		if err := advanceRunReader(h, readers, first.reader); err != nil {
			return nil, err
		}
		rec := first.rec
		if !groupOpen || rec.Field != currentField {
			if err := finishGroup(); err != nil {
				return nil, err
			}
			currentField = rec.Field
			if err := b.alignTo8(); err != nil {
				return nil, err
			}
			postingsOffset = b.offset
			dict.Reset()
			groupOpen = true
			b.beginBlock(plBlockName(currentField))
		}

		entries := []core.EntryID{rec.Entry}
		for h.Len() > 0 {
			next := (*h)[0]
			if next.rec.Field != rec.Field || next.rec.Term != rec.Term {
				break
			}
			item := heap.Pop(h).(postingHeapItem)
			entries = append(entries, item.rec.Entry)
			if err := advanceRunReader(h, readers, item.reader); err != nil {
				return nil, err
			}
		}
		dict.Add(rec.Term, PostingRef{Offset: b.offset - postingsOffset, Count: uint32(len(entries))})
		if err := b.writeBytes(WriteFlatPostingList(entries)); err != nil {
			return nil, err
		}
	}
	if err := finishGroup(); err != nil {
		return nil, err
	}
	return blockIndex, nil
}

// writeRangeBlocks serializes ext_range segment-tree blocks (all K merged)
// deterministically ordered by field and records them in blockIndex.
func (b *ExternalBuilder) writeRangeBlocks(blockIndex map[string]BlockDef) error {
	var fields []string
	for f := range b.rangeData {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, field := range fields {
		intervals := b.rangeData[field]
		rangeBytes, err := BuildRangeIndex(intervals)
		if err != nil {
			return fmt.Errorf("failed to build range index for field %s: %w", field, err)
		}
		if err := b.alignTo8(); err != nil {
			return err
		}
		name := containerBlockName(field, BlockKindRange)
		offset := b.offset
		if err := b.writeChecksummedBlock(name, rangeBytes); err != nil {
			return err
		}
		blockIndex[name] = BlockDef{Field: field, Kind: BlockKindRange, Offset: offset, Size: b.offset - offset}
	}
	return nil
}

func (b *ExternalBuilder) writeMergedRunRecords(w io.Writer, readers []*postingRunReader) error {
	bw := bufio.NewWriterSize(w, 64*1024)
	h := &postingRecordHeap{}
	heap.Init(h)
	for i, r := range readers {
		if ok, err := r.Next(); err != nil {
			return err
		} else if ok {
			heap.Push(h, postingHeapItem{rec: r.Record(), reader: i})
		}
	}
	for h.Len() > 0 {
		item := heap.Pop(h).(postingHeapItem)
		if err := b.writeRunRecord(bw, item.rec); err != nil {
			return err
		}
		if err := advanceRunReader(h, readers, item.reader); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func advanceRunReader(h *postingRecordHeap, readers []*postingRunReader, idx int) error {
	if ok, err := readers[idx].Next(); err != nil {
		return err
	} else if ok {
		heap.Push(h, postingHeapItem{rec: readers[idx].Record(), reader: idx})
	}
	return nil
}

func (b *ExternalBuilder) sortedFieldMeta() []FieldMetaDump {
	fields := make([]FieldMetaDump, 0, len(b.fields))
	for _, f := range b.fields {
		fields = append(fields, f)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

func (b *ExternalBuilder) writeBytes(data []byte) error {
	n, err := b.w.Write(data)
	b.offset += uint64(n)
	if b.currentBlockHash != nil && n > 0 {
		_, _ = b.currentBlockHash.Write(data[:n])
	}
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func (b *ExternalBuilder) writeChecksummedBlock(name string, data []byte) error {
	sum := sha256.Sum256(data)
	b.blockChecksums[name] = checksumPrefix + hex.EncodeToString(sum[:])
	return b.writeBytes(data)
}

func (b *ExternalBuilder) writeChecksummedFileBlock(name, path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = h.Write(chunk)
			if err := b.writeBytes(chunk); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	b.blockChecksums[name] = checksumPrefix + hex.EncodeToString(h.Sum(nil))
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
	b.currentBlockName = name
	b.currentBlockHash = sha256.New()
}

func (b *ExternalBuilder) finishBlock(name string) {
	if b.currentBlockHash == nil {
		return
	}
	if name == "" {
		name = b.currentBlockName
	}
	b.blockChecksums[name] = checksumPrefix + hex.EncodeToString(b.currentBlockHash.Sum(nil))
	b.currentBlockName = ""
	b.currentBlockHash = nil
}

func (b *ExternalBuilder) cleanupRuns() {
	for _, file := range b.runFiles {
		_ = os.Remove(file)
	}
	_ = os.RemoveAll(b.tmpDir)
	b.runFiles = nil
}

func lessPostingRecord(a, b postingRecord) bool {
	if a.Field != b.Field {
		return a.Field < b.Field
	}
	if a.Term != b.Term {
		return a.Term < b.Term
	}
	return a.Entry < b.Entry
}

// writeRunRecord serializes one posting record. The field is stored as a dense
// uint16 id (resolved via fieldID) instead of its full string name, which avoids
// On-disk run record format (compact, no K);
//   [fid uint16] [termLen uint32] [entry uint64] [term string bytes]
// = 14 bytes header, then term data. The field id (uint16) avoids
// repeating long field names on every record and shrinks run files / merge IO
// substantially. The term still varies per record and is stored inline.
func (b *ExternalBuilder) writeRunRecord(w io.Writer, rec postingRecord) error {
	fid, ok := b.fieldID[rec.Field]
	if !ok {
		return fmt.Errorf("run record references unknown field %q", rec.Field)
	}
	if len(rec.Term) > int(^uint32(0)) {
		return fmt.Errorf("run record term too large: %d", len(rec.Term))
	}
	header := make([]byte, 14)
	binary.LittleEndian.PutUint16(header[0:2], fid)
	binary.LittleEndian.PutUint32(header[2:6], uint32(len(rec.Term)))
	binary.LittleEndian.PutUint64(header[6:14], uint64(rec.Entry))
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := io.WriteString(w, rec.Term)
	return err
}

type postingRunReader struct {
	f      *os.File
	br     *bufio.Reader
	fields []string // dense id -> field name
	rec    postingRecord
}

func (b *ExternalBuilder) openPostingRunReader(path string) (*postingRunReader, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	return &postingRunReader{f: f, br: bufio.NewReaderSize(f, 64*1024), fields: b.fieldByID}, nil
}

func (r *postingRunReader) Next() (bool, error) {
	header := make([]byte, 14)
	if _, err := io.ReadFull(r.br, header); err != nil {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	fid := binary.LittleEndian.Uint16(header[0:2])
	termLen := binary.LittleEndian.Uint32(header[2:6])
	entry := binary.LittleEndian.Uint64(header[6:14])
	if int(fid) >= len(r.fields) {
		return false, fmt.Errorf("run record field id %d out of range", fid)
	}
	term := make([]byte, termLen)
	if _, err := io.ReadFull(r.br, term); err != nil {
		return false, err
	}
	r.rec = postingRecord{Field: r.fields[fid], Term: string(term), Entry: core.EntryID(entry)}
	return true, nil
}

func (r *postingRunReader) Record() postingRecord { return r.rec }

func (r *postingRunReader) Close() error { return r.f.Close() }

// streamingDict accumulates dict entries in merge order (already sorted by
// term) without using a map. Since the k-way merge emits records in strict
// (field, term) lexicographic order, terms arrive pre-sorted and are
// appended once — no lookup or deduplication needed. Bytes() produces output
// byte-identical to WriteFlatDict.
//
// Compared to map[string]PostingRef, this avoids per-entry map bucket overhead
// (~50B/entry) and stores term bytes contiguously instead of as separate string
// allocations, reducing GC pressure and peak memory by ~50–60% for large groups.
// Items and terms are still held in memory until Bytes() is called, so peak is
// O(distinct terms) rather than O(1); for fields with millions of distinct terms
// a two-temp-file streaming approach could further reduce peak at the cost of
// extra IO.
type streamingDict struct {
	items []streamingDictItem
	terms []byte // concatenated term bytes in arrival (= sorted) order
}

// streamingDictItem records one term's position in the concatenated term buffer
// and its associated PostingRef fields. termLen is stored explicitly (rather
// than derived from the next item's termStart) so that AC field compilation can
// reconstruct the term string without scanning.
type streamingDictItem struct {
	termStart    uint32 // byte offset of this term within streamingDict.terms
	termLen      uint32 // byte length of the term string
	postingCount uint32 // number of EntryIDs in this term's posting list
	postingOff   uint64 // block-relative byte offset of the posting list header
}

// Add appends a term and its PostingRef. Callers must ensure terms are added
// in lexicographic order — the k-way merge naturally guarantees this, so no
// sorting or deduplication is performed.
func (d *streamingDict) Add(term string, ref PostingRef) {
	d.items = append(d.items, streamingDictItem{
		termStart:    uint32(len(d.terms)),
		termLen:      uint32(len(term)),
		postingCount: ref.Count,
		postingOff:   ref.Offset,
	})
	d.terms = append(d.terms, term...)
}

// Bytes serializes the accumulated entries into the FlatDict binary format.
// The output is byte-identical to WriteFlatDict when terms are added in sorted
// order. Layout:
//
//	[Count          (uint32)]  — number of entries
//	[TermDataOffset (uint32)]  — byte offset where term strings begin
//	repeated Count times:
//	  [KeyOffset     (uint32)] — absolute byte offset of term within the block
//	  [PostingCount  (uint32)] — number of EntryIDs in the posting list
//	  [PostingOffset (uint64)] — block-relative offset of the posting list
//	[TermData ...]             — concatenated term bytes
//
// keyOffset = TermDataOffset + item.termStart, where item.termStart is the
// term's offset within d.terms (the concatenated term buffer). This matches
// WriteFlatDict's curStringOffset which starts at TermDataOffset and advances
// by each term's length.
func (d *streamingDict) Bytes() []byte {
	count := uint32(len(d.items))
	itemsAreaSize := int(count) * flatDictItemSize
	buf := make([]byte, flatDictHeaderSize+itemsAreaSize+len(d.terms))

	binary.LittleEndian.PutUint32(buf[0:4], count)
	termDataOffset := uint32(flatDictHeaderSize + itemsAreaSize)
	binary.LittleEndian.PutUint32(buf[4:8], termDataOffset)

	for i, item := range d.items {
		off := flatDictHeaderSize + i*flatDictItemSize
		keyOffset := termDataOffset + item.termStart
		binary.LittleEndian.PutUint32(buf[off:off+4], keyOffset)
		binary.LittleEndian.PutUint32(buf[off+4:off+8], item.postingCount)
		binary.LittleEndian.PutUint64(buf[off+8:off+16], item.postingOff)
	}
	copy(buf[termDataOffset:], d.terms)
	return buf
}

// Reset clears the dict for reuse across (K, field) groups. The underlying
// arrays are retained ([:0] instead of nil) so subsequent groups can reuse
// the allocated capacity without new allocations.
func (d *streamingDict) Reset() {
	d.items = d.items[:0]
	d.terms = d.terms[:0]
}

type postingHeapItem struct {
	rec    postingRecord
	reader int
}

type postingRecordHeap []postingHeapItem

func (h postingRecordHeap) Len() int { return len(h) }

func (h postingRecordHeap) Less(i, j int) bool { return lessPostingRecord(h[i].rec, h[j].rec) }

func (h postingRecordHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *postingRecordHeap) Push(x interface{}) { *h = append(*h, x.(postingHeapItem)) }

func (h *postingRecordHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
