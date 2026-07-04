package builder

import (
	"container/heap"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/echoface/be_indexer/core"
)

const (
	entriesSidecarMagicForBuild = "BEIENT1\x00"
	defaultMaxEntriesInMemory   = 1_000_000
	maxOpenEntryRunReaders      = 64
)

type entryRunAccumulator struct {
	tmpDir string
	max    int
	buf    core.Entries
	runs   []string
	count  uint64
}

func newEntryRunAccumulator(tmpDir string, maxEntriesInMemory int) *entryRunAccumulator {
	if maxEntriesInMemory <= 0 {
		maxEntriesInMemory = defaultMaxEntriesInMemory
	}
	return &entryRunAccumulator{
		tmpDir: tmpDir,
		max:    maxEntriesInMemory,
		buf:    make(core.Entries, 0, maxEntriesInMemory),
	}
}

func (a *entryRunAccumulator) Add(entries core.Entries) error {
	for _, entry := range entries {
		a.buf = append(a.buf, entry)
		a.count++
		if len(a.buf) >= a.max {
			if err := a.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *entryRunAccumulator) WriteSidecar(dir, name string) (string, string, error) {
	if err := a.flush(); err != nil {
		return "", "", err
	}
	if err := a.compactRuns(); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dir, ".entries-*.tmp")
	if err != nil {
		return "", "", err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	w := io.MultiWriter(tmp, h)
	header := make([]byte, 16)
	copy(header[:8], entriesSidecarMagicForBuild)
	binary.LittleEndian.PutUint64(header[8:16], a.count)
	if _, err := w.Write(header); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := a.writeMergedEntries(w); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		return "", "", err
	}
	committed = true
	return name, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (a *entryRunAccumulator) Cleanup() {
	for _, run := range a.runs {
		_ = os.Remove(run)
	}
	_ = os.RemoveAll(a.tmpDir)
	a.runs = nil
}

func (a *entryRunAccumulator) flush() error {
	if len(a.buf) == 0 {
		return nil
	}
	sort.Slice(a.buf, func(i, j int) bool { return a.buf[i] < a.buf[j] })
	if err := os.MkdirAll(a.tmpDir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(a.tmpDir, "entry-run-*.bin")
	if err != nil {
		return err
	}
	name := f.Name()
	for _, entry := range a.buf {
		if err := writeEntryRunRecord(f, entry); err != nil {
			_ = f.Close()
			_ = os.Remove(name)
			return err
		}
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
	a.runs = append(a.runs, name)
	a.buf = a.buf[:0]
	return nil
}

func (a *entryRunAccumulator) compactRuns() error {
	for len(a.runs) > maxOpenEntryRunReaders {
		var next []string
		for start := 0; start < len(a.runs); start += maxOpenEntryRunReaders {
			end := start + maxOpenEntryRunReaders
			if end > len(a.runs) {
				end = len(a.runs)
			}
			merged, err := a.mergeRunBatch(a.runs[start:end])
			if err != nil {
				return err
			}
			next = append(next, merged)
			for _, run := range a.runs[start:end] {
				_ = os.Remove(run)
			}
		}
		a.runs = next
	}
	return nil
}

func (a *entryRunAccumulator) mergeRunBatch(files []string) (string, error) {
	out, err := os.CreateTemp(a.tmpDir, "entry-run-merged-*.bin")
	if err != nil {
		return "", err
	}
	name := out.Name()
	readers, err := openEntryRunReaders(files)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(name)
		return "", err
	}
	defer closeEntryRunReaders(readers)
	if err := writeMergedEntryRuns(out, readers); err != nil {
		_ = out.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func (a *entryRunAccumulator) writeMergedEntries(w io.Writer) error {
	readers, err := openEntryRunReaders(a.runs)
	if err != nil {
		return err
	}
	defer closeEntryRunReaders(readers)
	return writeMergedEntryRuns(w, readers)
}

func writeEntryRunRecord(w io.Writer, entry core.EntryID) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(entry))
	_, err := w.Write(buf[:])
	return err
}

type entryRunReader struct {
	f     *os.File
	entry core.EntryID
}

func openEntryRunReaders(files []string) ([]*entryRunReader, error) {
	readers := make([]*entryRunReader, 0, len(files))
	for _, file := range files {
		f, err := os.Open(filepath.Clean(file))
		if err != nil {
			closeEntryRunReaders(readers)
			return nil, err
		}
		readers = append(readers, &entryRunReader{f: f})
	}
	return readers, nil
}

func closeEntryRunReaders(readers []*entryRunReader) {
	for _, r := range readers {
		_ = r.f.Close()
	}
}

func (r *entryRunReader) Next() (bool, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r.f, buf[:]); err != nil {
		if err == io.EOF {
			return false, nil
		}
		return false, err
	}
	r.entry = core.EntryID(binary.LittleEndian.Uint64(buf[:]))
	return true, nil
}

type entryHeapItem struct {
	entry  core.EntryID
	reader int
}

type entryHeap []entryHeapItem

func (h entryHeap) Len() int { return len(h) }
func (h entryHeap) Less(i, j int) bool {
	return h[i].entry < h[j].entry
}
func (h entryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x interface{}) {
	*h = append(*h, x.(entryHeapItem))
}
func (h *entryHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func writeMergedEntryRuns(w io.Writer, readers []*entryRunReader) error {
	h := &entryHeap{}
	heap.Init(h)
	for i, r := range readers {
		if ok, err := r.Next(); err != nil {
			return err
		} else if ok {
			heap.Push(h, entryHeapItem{entry: r.entry, reader: i})
		}
	}
	for h.Len() > 0 {
		item := heap.Pop(h).(entryHeapItem)
		if err := writeEntryRunRecord(w, item.entry); err != nil {
			return err
		}
		if ok, err := readers[item.reader].Next(); err != nil {
			return err
		} else if ok {
			heap.Push(h, entryHeapItem{entry: readers[item.reader].entry, reader: item.reader})
		}
	}
	return nil
}

func (a *entryRunAccumulator) String() string {
	return fmt.Sprintf("entryRunAccumulator{count:%d,runs:%d,buf:%d}", a.count, len(a.runs), len(a.buf))
}
