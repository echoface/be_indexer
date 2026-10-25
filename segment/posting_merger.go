package segment

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// PostingGroup is a sorted-output unit produced by SortedPostingMerger: one
// opaque key plus every EntryID accumulated under it.
type PostingGroup struct {
	Key     []byte
	Entries []core.EntryID
}

// SortedPostingMerger groups EntryIDs by an opaque byte key, then emits the
// groups in ascending key order via a k-way merge. When the distinct-key count
// exceeds maxRecs it spills sorted runs to tmpDir, bounding peak memory. The
// key's semantics (term, encoded interval, ...) belong to the caller.
type SortedPostingMerger struct {
	pending  map[string]int
	groups   []PostingGroup
	maxRecs  int
	tmpDir   string
	runFiles []string
}

// NewSortedPostingMerger creates a merger that spills to tmpDir after maxRecs
// distinct keys; maxRecs <= 0 means effectively unbounded (memory-only).
func NewSortedPostingMerger(maxRecs int, tmpDir string) *SortedPostingMerger {
	c := &SortedPostingMerger{
		pending: make(map[string]int),
		maxRecs: maxRecs,
		tmpDir:  tmpDir,
	}
	if c.maxRecs <= 0 {
		c.maxRecs = 1 << 30
	}
	return c
}

func (c *SortedPostingMerger) Add(key []byte, entries []core.EntryID) error {
	ks := string(key)
	idx, ok := c.pending[ks]
	if ok {
		c.groups[idx].Entries = append(c.groups[idx].Entries, entries...)
	} else {
		rec := PostingGroup{Key: make([]byte, len(key)), Entries: make([]core.EntryID, len(entries))}
		copy(rec.Key, key)
		copy(rec.Entries, entries)
		c.pending[ks] = len(c.groups)
		c.groups = append(c.groups, rec)
	}
	if len(c.groups) >= c.maxRecs {
		return c.flushRun()
	}
	return nil
}

func (c *SortedPostingMerger) flushRun() error {
	if len(c.groups) == 0 {
		return nil
	}
	sort.Slice(c.groups, func(i, j int) bool {
		return bytes.Compare(c.groups[i].Key, c.groups[j].Key) < 0
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
	for _, rec := range c.groups {
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
	c.groups = c.groups[:0]
	return nil
}

const maxKeyedOpenRuns = 64

// MergeIter is a pull-based cursor over the merger's sorted, key-merged
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
	groups  []PostingGroup
	pos     int

	// spill mode: k-way merge across open run readers.
	readers  []*keyedRunReader
	h        *keyedRecordHeap
	runFiles []string

	cur PostingGroup
	err error
}

// MergeIter finalizes accumulation (flushing any pending run and collapsing the
// fan-in below maxKeyedOpenRuns) and returns a streaming cursor over the merged
// records. Ownership of the merger's spill files transfers to the iterator,
// which removes them on Close.
func (c *SortedPostingMerger) MergeIter() (*MergeIter, error) {
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
		// In-memory mode: c.groups is already sorted (flushRun) and unique (Add
		// dedups via the pending map), so we walk it directly with zero extra copy.
		return &MergeIter{memMode: true, groups: c.groups}, nil
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
		if err := r.Err(); err != nil {
			_ = it.Close()
			return nil, err
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
		if it.pos >= len(it.groups) {
			return false
		}
		it.cur = it.groups[it.pos]
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
	nextReader := it.readers[first.reader]
	if nextReader.Next() {
		heap.Push(h, keyedHeapItem{rec: nextReader.rec, reader: first.reader})
	}
	if err := nextReader.Err(); err != nil {
		it.err = err
		return false
	}
	for h.Len() > 0 {
		next := (*h)[0]
		if !bytes.Equal(merged.Key, next.rec.Key) {
			break
		}
		item := heap.Pop(h).(keyedHeapItem)
		merged.Entries = append(merged.Entries, item.rec.Entries...)
		nextReader = it.readers[item.reader]
		if nextReader.Next() {
			heap.Push(h, keyedHeapItem{rec: nextReader.rec, reader: item.reader})
		}
		if err := nextReader.Err(); err != nil {
			it.err = err
			return false
		}
	}
	it.cur = merged
	return true
}

// Record returns the current merged record. Valid only until the next Next.
func (it *MergeIter) Record() PostingGroup { return it.cur }

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

// Merge drains MergeIter into a single slice. In memory mode it returns
// c.groups without an extra copy; use MergeIter for bounded streaming output.
func (c *SortedPostingMerger) Merge() ([]PostingGroup, error) {
	it, err := c.MergeIter()
	if err != nil {
		return nil, err
	}
	defer it.Close()
	if it.memMode {
		return it.groups, nil
	}
	var result []PostingGroup
	for it.Next() {
		result = append(result, it.Record())
	}
	return result, it.Err()
}

func (c *SortedPostingMerger) mergeRunBatch(files []string) (string, error) {
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

func (c *SortedPostingMerger) writeMergedRun(w io.Writer, readers []*keyedRunReader) error {
	h := &keyedRecordHeap{}
	heap.Init(h)
	for i, r := range readers {
		if r.hasNext {
			heap.Push(h, keyedHeapItem{rec: r.rec, reader: i})
		}
		if err := r.Err(); err != nil {
			return err
		}
	}
	for h.Len() > 0 {
		item := heap.Pop(h).(keyedHeapItem)
		if err := writeKeyedRunRecord(w, item.rec); err != nil {
			return err
		}
		nextReader := readers[item.reader]
		if nextReader.Next() {
			heap.Push(h, keyedHeapItem{rec: nextReader.rec, reader: item.reader})
		}
		if err := nextReader.Err(); err != nil {
			return err
		}
	}
	return nil
}

// writeKeyedRunRecord writes one keyed record to a spill file.
// Format: [keyLen uint32] [entryCount uint32] [entryID uint64 × entryCount] [key bytes]
func writeKeyedRunRecord(w io.Writer, rec PostingGroup) error {
	keyLen, err := checkedU32(len(rec.Key), "spill key length")
	if err != nil {
		return err
	}
	entryCount, err := checkedU32(len(rec.Entries), "spill entry count")
	if err != nil {
		return err
	}
	var header [8]byte
	binary.LittleEndian.PutUint32(header[0:4], keyLen)
	binary.LittleEndian.PutUint32(header[4:8], entryCount)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	buf := make([]byte, entryCount*8)
	for i, e := range rec.Entries {
		binary.LittleEndian.PutUint64(buf[i*8:], uint64(e))
	}
	if _, err := w.Write(buf); err != nil {
		return err
	}
	_, werr := w.Write(rec.Key)
	return werr
}

type keyedRunReader struct {
	f       *os.File
	hasNext bool
	rec     PostingGroup
	err     error
	path    string
	offset  int64
}

func openKeyedRunReader(path string) (*keyedRunReader, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	r := &keyedRunReader{f: f}
	r.path = path
	r.advance()
	if r.err != nil {
		_ = f.Close()
		return nil, r.err
	}
	return r, nil
}

// advance reads the next record. A clean end-of-file yields hasNext=false with
// a nil error; a partial record (ErrUnexpectedEOF) or an IO failure sets err.
// err is sticky: once set, advance is a no-op.
func (r *keyedRunReader) advance() {
	if r.err != nil {
		r.hasNext = false
		return
	}
	var header [8]byte
	n, err := io.ReadFull(r.f, header[:])
	r.offset += int64(n)
	if err != nil {
		if errors.Is(err, io.EOF) {
			if r.offset == 0 {
				r.setErr(errors.New("empty spill run"))
			}
			r.hasNext = false
			return // clean EOF: no partial record pending
		}
		r.setErr(err)
		return
	}
	keyLen := binary.LittleEndian.Uint32(header[0:4])
	entryCount := binary.LittleEndian.Uint32(header[4:8])

	// Validate the header against the remaining file size before allocating.
	// A corrupt count must surface as a build error, not an OOM panic.
	need := int64(uint64(entryCount))*8 + int64(keyLen)
	if info, statErr := r.f.Stat(); statErr != nil {
		r.setErr(statErr)
		return
	} else if remain := info.Size() - r.offset; need > remain {
		r.setErr(fmt.Errorf(
			"corrupt run record header at offset %d: needs %d bytes, only %d remain in %s",
			r.offset-8, need, remain, r.path,
		))
		return
	}

	entryBytes := make([]byte, int64(entryCount)*8)
	n, err = io.ReadFull(r.f, entryBytes)
	r.offset += int64(n)
	if err != nil {
		r.setErr(err)
		return
	}
	entries := make([]core.EntryID, entryCount)
	for i := range entries {
		entries[i] = core.EntryID(binary.LittleEndian.Uint64(entryBytes[i*8:]))
	}

	key := make([]byte, keyLen)
	n, err = io.ReadFull(r.f, key)
	r.offset += int64(n)
	if err != nil {
		r.setErr(err)
		return
	}

	r.rec.Key = key
	r.rec.Entries = entries
	r.hasNext = true
}

func (r *keyedRunReader) setErr(err error) {
	r.err = fmt.Errorf("read spill run %s at offset %d: %w", r.path, r.offset, err)
	r.hasNext = false
}

func (r *keyedRunReader) Next() bool {
	r.advance()
	return r.hasNext
}

func (r *keyedRunReader) Err() error { return r.err }

func (r *keyedRunReader) Close() error { return r.f.Close() }

type keyedHeapItem struct {
	rec    PostingGroup
	reader int
}

type keyedRecordHeap []keyedHeapItem

func (h keyedRecordHeap) Len() int            { return len(h) }
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
