package segment

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// KeyedRecord is a sorted-output unit produced by KeyedPostingCollector.
type KeyedRecord struct {
	Key     []byte
	Entries []core.EntryID
}

// KeyedPostingCollector accumulates key → []EntryID mappings with optional
// spill to disk when the distinct-key count exceeds maxRecs. Merge() returns
// all records in sorted-key order via k-way merge.
type KeyedPostingCollector struct {
	pending  map[string]int
	records  []KeyedRecord
	maxRecs  int
	tmpDir   string
	runFiles []string
}

func NewKeyedPostingCollector(maxRecs int, tmpDir string) *KeyedPostingCollector {
	c := &KeyedPostingCollector{
		pending: make(map[string]int),
		maxRecs: maxRecs,
		tmpDir:  tmpDir,
	}
	if c.maxRecs <= 0 {
		c.maxRecs = 1 << 30
	}
	return c
}

func (c *KeyedPostingCollector) Add(key []byte, entries []core.EntryID) error {
	ks := string(key)
	idx, ok := c.pending[ks]
	if ok {
		c.records[idx].Entries = append(c.records[idx].Entries, entries...)
	} else {
		rec := KeyedRecord{Key: make([]byte, len(key)), Entries: make([]core.EntryID, len(entries))}
		copy(rec.Key, key)
		copy(rec.Entries, entries)
		c.pending[ks] = len(c.records)
		c.records = append(c.records, rec)
	}
	if len(c.records) >= c.maxRecs {
		return c.flushRun()
	}
	return nil
}

func (c *KeyedPostingCollector) flushRun() error {
	if len(c.records) == 0 {
		return nil
	}
	sort.Slice(c.records, func(i, j int) bool {
		return bytes.Compare(c.records[i].Key, c.records[j].Key) < 0
	})
	if c.tmpDir == "" {
		return nil
	}
	if err := os.MkdirAll(c.tmpDir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(c.tmpDir, "kpc-run-*.bin")
	if err != nil {
		return err
	}
	name := f.Name()
	for _, rec := range c.records {
		if err := writeKeyedRunRecord(f, rec); err != nil {
			f.Close()
			os.Remove(name)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	c.runFiles = append(c.runFiles, name)
	c.pending = make(map[string]int)
	c.records = c.records[:0]
	return nil
}

const maxKeyedOpenRuns = 64

// MergeIter is a pull-based cursor over the collector's sorted, key-merged
// output. It yields each distinct key exactly once, with all its EntryIDs
// concatenated, in ascending key order — the same sequence Merge() produces,
// but one record at a time so callers never hold the whole result in memory.
//
// Ownership: each Record() return is valid until the next Next() call. Both the
// Key and Entries slices are freshly produced per record, so callers may retain
// them. Close() MUST be called to release spill file handles and remove temp
// runs; it is safe to call multiple times.
type MergeIter struct {
	// memMode iterates pre-sorted, already-deduped records in place (no spill).
	memMode bool
	records []KeyedRecord
	pos     int

	// spill mode: k-way merge across open run readers.
	readers  []*keyedRunReader
	h        *keyedRecordHeap
	runFiles []string

	cur KeyedRecord
	err error
}

// MergeIter finalizes accumulation (flushing any pending run and collapsing the
// fan-in below maxKeyedOpenRuns) and returns a streaming cursor over the merged
// records. Ownership of the collector's spill files transfers to the iterator,
// which removes them on Close.
func (c *KeyedPostingCollector) MergeIter() (*MergeIter, error) {
	if err := c.flushRun(); err != nil {
		return nil, err
	}
	for len(c.runFiles) > maxKeyedOpenRuns {
		var next []string
		for start := 0; start < len(c.runFiles); start += maxKeyedOpenRuns {
			end := start + maxKeyedOpenRuns
			if end > len(c.runFiles) {
				end = len(c.runFiles)
			}
			merged, err := c.mergeRunBatch(c.runFiles[start:end])
			if err != nil {
				return nil, err
			}
			next = append(next, merged)
			for _, file := range c.runFiles[start:end] {
				os.Remove(file)
			}
		}
		c.runFiles = next
	}

	if len(c.runFiles) == 0 {
		// In-memory mode: c.records is already sorted (flushRun) and unique (Add
		// dedups via the pending map), so we walk it directly with zero extra copy.
		return &MergeIter{memMode: true, records: c.records}, nil
	}

	it := &MergeIter{runFiles: c.runFiles}
	c.runFiles = nil // ownership transfers to the iterator; freed on Close.
	for _, file := range it.runFiles {
		r, err := openKeyedRunReader(file)
		if err != nil {
			it.Close()
			return nil, err
		}
		it.readers = append(it.readers, r)
	}
	h := &keyedRecordHeap{}
	heap.Init(h)
	for i, r := range it.readers {
		if r.hasNext {
			heap.Push(h, keyedHeapItem{rec: r.rec, reader: i})
		}
	}
	it.h = h
	return it, nil
}

// Next advances to the next merged record, returning false at end of stream.
func (it *MergeIter) Next() bool {
	if it.err != nil {
		return false
	}
	if it.memMode {
		if it.pos >= len(it.records) {
			return false
		}
		it.cur = it.records[it.pos]
		it.pos++
		return true
	}
	h := it.h
	if h.Len() == 0 {
		return false
	}
	// Pop the smallest key, then fold in every heap entry sharing that key. Each
	// keyedRunReader.Next allocates a fresh rec, so the popped record's slices
	// stay valid while we append onto them.
	first := heap.Pop(h).(keyedHeapItem)
	merged := first.rec
	if it.readers[first.reader].Next() {
		heap.Push(h, keyedHeapItem{rec: it.readers[first.reader].rec, reader: first.reader})
	}
	for h.Len() > 0 {
		next := (*h)[0]
		if !bytes.Equal(merged.Key, next.rec.Key) {
			break
		}
		item := heap.Pop(h).(keyedHeapItem)
		merged.Entries = append(merged.Entries, item.rec.Entries...)
		if it.readers[item.reader].Next() {
			heap.Push(h, keyedHeapItem{rec: it.readers[item.reader].rec, reader: item.reader})
		}
	}
	it.cur = merged
	return true
}

// Record returns the current merged record. Valid only until the next Next.
func (it *MergeIter) Record() KeyedRecord { return it.cur }

// Err returns any error encountered during iteration.
func (it *MergeIter) Err() error { return it.err }

// Close releases run readers and removes spill files. Safe to call repeatedly.
func (it *MergeIter) Close() error {
	var firstErr error
	for _, r := range it.readers {
		if err := r.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	it.readers = nil
	for _, file := range it.runFiles {
		os.Remove(file)
	}
	it.runFiles = nil
	return firstErr
}

// Merge drains MergeIter into a single slice. It preserves the historical
// behavior (and, in memory mode, the zero-extra-copy return of c.records) for
// callers that still want the whole result at once.
func (c *KeyedPostingCollector) Merge() ([]KeyedRecord, error) {
	it, err := c.MergeIter()
	if err != nil {
		return nil, err
	}
	defer it.Close()
	if it.memMode {
		return it.records, nil
	}
	var result []KeyedRecord
	for it.Next() {
		result = append(result, it.Record())
	}
	return result, it.Err()
}

func (c *KeyedPostingCollector) mergeRunBatch(files []string) (string, error) {
	readers := make([]*keyedRunReader, 0, len(files))
	defer func() {
		for _, r := range readers {
			r.Close()
		}
	}()
	for _, file := range files {
		r, err := openKeyedRunReader(file)
		if err != nil {
			return "", err
		}
		readers = append(readers, r)
	}
	out, err := os.CreateTemp(c.tmpDir, "kpc-merge-*.bin")
	if err != nil {
		return "", err
	}
	outName := out.Name()
	if err := c.writeMergedRun(out, readers); err != nil {
		out.Close()
		os.Remove(outName)
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(outName)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(outName)
		return "", err
	}
	return outName, nil
}

func (c *KeyedPostingCollector) writeMergedRun(w io.Writer, readers []*keyedRunReader) error {
	h := &keyedRecordHeap{}
	heap.Init(h)
	for i, r := range readers {
		if r.hasNext {
			heap.Push(h, keyedHeapItem{rec: r.rec, reader: i})
		}
	}
	for h.Len() > 0 {
		item := heap.Pop(h).(keyedHeapItem)
		if err := writeKeyedRunRecord(w, item.rec); err != nil {
			return err
		}
		if readers[item.reader].Next() {
			heap.Push(h, keyedHeapItem{rec: readers[item.reader].rec, reader: item.reader})
		}
	}
	return nil
}

// writeKeyedRunRecord writes one keyed record to a spill file.
// Format: [keyLen uint32] [entryCount uint32] [entryID uint64 × entryCount] [key bytes]
func writeKeyedRunRecord(w io.Writer, rec KeyedRecord) error {
	header := make([]byte, 8)
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(rec.Key)))
	binary.LittleEndian.PutUint32(header[4:8], uint32(len(rec.Entries)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	buf := make([]byte, len(rec.Entries)*8)
	for i, e := range rec.Entries {
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(e))
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	_, err := w.Write(rec.Key)
	return err
}

type keyedRunReader struct {
	f       *os.File
	hasNext bool
	rec     KeyedRecord
}

func openKeyedRunReader(path string) (*keyedRunReader, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	r := &keyedRunReader{f: f}
	r.hasNext = r.readNext()
	return r, nil
}

func (r *keyedRunReader) readNext() bool {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r.f, header); err != nil {
		return false
	}
	keyLen := binary.LittleEndian.Uint32(header[0:4])
	entryCount := binary.LittleEndian.Uint32(header[4:8])
	entryBytes := make([]byte, entryCount*8)
	if _, err := io.ReadFull(r.f, entryBytes); err != nil {
		return false
	}
	r.rec.Entries = make([]core.EntryID, entryCount)
	for i := range r.rec.Entries {
		r.rec.Entries[i] = core.EntryID(binary.LittleEndian.Uint64(entryBytes[i*8:]))
	}
	r.rec.Key = make([]byte, keyLen)
	if _, err := io.ReadFull(r.f, r.rec.Key); err != nil {
		return false
	}
	return true
}

func (r *keyedRunReader) Next() bool {
	return r.readNext()
}

func (r *keyedRunReader) Close() error { return r.f.Close() }

type keyedHeapItem struct {
	rec    KeyedRecord
	reader int
}

type keyedRecordHeap []keyedHeapItem

func (h keyedRecordHeap) Len() int           { return len(h) }
func (h keyedRecordHeap) Less(i, j int) bool  { return bytes.Compare(h[i].rec.Key, h[j].rec.Key) < 0 }
func (h keyedRecordHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *keyedRecordHeap) Push(x interface{}) { *h = append(*h, x.(keyedHeapItem)) }
func (h *keyedRecordHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
