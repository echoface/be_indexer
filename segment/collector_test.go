package segment

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/echoface/be_indexer/core"
)

// drainIter collects a MergeIter into a slice for comparison. Entries within
// each record are sorted: the collector guarantees ascending key order and the
// full entry multiset per key, but NOT intra-key entry order (memory mode keeps
// insertion order; spill mode yields heap-pop order). Every real consumer sorts
// entries before use, so normalizing here compares the actual contract.
func drainIter(t *testing.T, it *MergeIter) []KeyedRecord {
	t.Helper()
	var out []KeyedRecord
	for it.Next() {
		rec := it.Record()
		// Copy because Record() is only valid until the next Next().
		k := append([]byte(nil), rec.Key...)
		e := append([]core.EntryID(nil), rec.Entries...)
		sort.Slice(e, func(i, j int) bool { return e[i] < e[j] })
		out = append(out, KeyedRecord{Key: k, Entries: e})
	}
	if err := it.Err(); err != nil {
		t.Fatalf("iter err: %v", err)
	}
	if err := it.Close(); err != nil {
		t.Fatalf("iter close: %v", err)
	}
	return out
}

// feed adds a fixed, intentionally out-of-order, duplicate-key dataset. The same
// input drives both the in-memory and the forced-spill collector so their merged
// output must be byte-identical.
func feed(c *KeyedPostingCollector) error {
	adds := []struct {
		key     string
		entries []core.EntryID
	}{
		{"banana", []core.EntryID{5, 1}},
		{"apple", []core.EntryID{9}},
		{"cherry", []core.EntryID{2, 8, 4}},
		{"apple", []core.EntryID{3, 7}}, // dup key, merges with first apple
		{"banana", []core.EntryID{6}},   // dup key across a potential spill boundary
		{"date", []core.EntryID{0}},
		{"cherry", []core.EntryID{1}},
	}
	for _, a := range adds {
		if err := c.Add([]byte(a.key), a.entries); err != nil {
			return err
		}
	}
	return nil
}

func TestMergeIter_MemoryVsSpillEquivalence(t *testing.T) {
	// In-memory: never spills (huge maxRecs).
	mem := NewKeyedPostingCollector(1<<20, "")
	if err := feed(mem); err != nil {
		t.Fatal(err)
	}
	memIt, err := mem.MergeIter()
	if err != nil {
		t.Fatal(err)
	}
	if !memIt.memMode {
		t.Fatalf("expected in-memory mode with large maxRecs")
	}
	memOut := drainIter(t, memIt)

	// Forced spill: maxRecs=1 flushes a run on nearly every distinct key, so
	// Merge must reconstruct order and dedup purely from the k-way merge.
	spill := NewKeyedPostingCollector(1, t.TempDir())
	if err := feed(spill); err != nil {
		t.Fatal(err)
	}
	spillIt, err := spill.MergeIter()
	if err != nil {
		t.Fatal(err)
	}
	if spillIt.memMode {
		t.Fatalf("expected spill mode with maxRecs=1")
	}
	spillOut := drainIter(t, spillIt)

	if !reflect.DeepEqual(memOut, spillOut) {
		t.Fatalf("mem vs spill mismatch:\n mem=%v\n spill=%v", memOut, spillOut)
	}

	// Verify the expected content explicitly: keys ascending, entries merged
	// (sorted by drainIter, since intra-key order is unspecified).
	want := []KeyedRecord{
		{Key: []byte("apple"), Entries: []core.EntryID{3, 7, 9}},
		{Key: []byte("banana"), Entries: []core.EntryID{1, 5, 6}},
		{Key: []byte("cherry"), Entries: []core.EntryID{1, 2, 4, 8}},
		{Key: []byte("date"), Entries: []core.EntryID{0}},
	}
	if !reflect.DeepEqual(memOut, want) {
		t.Fatalf("unexpected merged content:\n got=%v\n want=%v", memOut, want)
	}
}

// TestMergeIter_ManyRunsFanIn forces more than maxKeyedOpenRuns spilled runs so
// the recursive batch pre-merge path (MergeIter's >maxKeyedOpenRuns loop) runs.
func TestMergeIter_ManyRunsFanIn(t *testing.T) {
	const n = maxKeyedOpenRuns*3 + 5
	spill := NewKeyedPostingCollector(1, t.TempDir())
	for i := 0; i < n; i++ {
		// keys chosen so ordering after merge is well-defined and unique.
		key := []byte{byte('A' + i/26), byte('a' + i%26)}
		if err := spill.Add(key, []core.EntryID{core.EntryID(i)}); err != nil {
			t.Fatal(err)
		}
	}
	it, err := spill.MergeIter()
	if err != nil {
		t.Fatal(err)
	}
	out := drainIter(t, it)
	if len(out) != n {
		t.Fatalf("expected %d distinct keys, got %d", n, len(out))
	}
	// Confirm ascending key order survived the multi-level fan-in merge.
	for i := 1; i < len(out); i++ {
		if string(out[i-1].Key) >= string(out[i].Key) {
			t.Fatalf("keys not ascending at %d: %q >= %q", i, out[i-1].Key, out[i].Key)
		}
	}
}

// TestMerge_WrapperMatchesIter ensures the retained Merge() wrapper yields the
// same result as draining MergeIter directly, in both modes.
func TestMerge_WrapperMatchesIter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		maxRecs int
		tmp     bool
	}{
		{"memory", 1 << 20, false},
		{"spill", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := ""
			if tc.tmp {
				tmpDir = t.TempDir()
			}
			cIter := NewKeyedPostingCollector(tc.maxRecs, tmpDir)
			if err := feed(cIter); err != nil {
				t.Fatal(err)
			}
			it, err := cIter.MergeIter()
			if err != nil {
				t.Fatal(err)
			}
			viaIter := drainIter(t, it)

			cMerge := NewKeyedPostingCollector(tc.maxRecs, tmpDir)
			if err := feed(cMerge); err != nil {
				t.Fatal(err)
			}
			viaMerge, err := cMerge.Merge()
			if err != nil {
				t.Fatal(err)
			}
			// Normalize intra-key order to match drainIter's sorting.
			for _, rec := range viaMerge {
				sort.Slice(rec.Entries, func(i, j int) bool { return rec.Entries[i] < rec.Entries[j] })
			}
			if !reflect.DeepEqual(viaIter, viaMerge) {
				t.Fatalf("Merge() vs MergeIter mismatch:\n iter=%v\n merge=%v", viaIter, viaMerge)
			}
		})
	}
}

// spillTwoRuns creates a collector forced to spill exactly two sorted runs and
// returns the collector plus its two run file paths.
func spillTwoRuns(t *testing.T) (*KeyedPostingCollector, []string) {
	t.Helper()
	c := NewKeyedPostingCollector(1, t.TempDir())
	for _, kv := range []struct {
		key string
		eid core.EntryID
	}{
		{"apple", 1}, {"banana", 2}, // two distinct keys -> two runs
	} {
		if err := c.Add([]byte(kv.key), []core.EntryID{kv.eid}); err != nil {
			t.Fatal(err)
		}
	}
	return c, c.runFiles
}

func TestMergeIter_TruncatedRunReturnsError(t *testing.T) {
	for _, cut := range []struct {
		name string
		n    int64
	}{
		{"mid_header", 4},
		{"mid_entries", 12},
		{"mid_key", 17},
	} {
		t.Run(cut.name, func(t *testing.T) {
			c, runs := spillTwoRuns(t)
			if len(runs) != 2 {
				t.Fatalf("expected 2 runs, got %d", len(runs))
			}
			full, err := os.Stat(runs[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(runs[0], full.Size()-cut.n); err != nil {
				t.Fatal(err)
			}

			it, err := c.MergeIter()
			if err == nil {
				for it.Next() {
				}
				err = it.Err()
				_ = it.Close()
			}
			if err == nil {
				t.Fatal("expected truncated run to surface an error")
			}
		})
	}
}

func TestMergeIter_CorruptHugeEntryCountReturnsErrorNotOOM(t *testing.T) {
	c, runs := spillTwoRuns(t)
	if len(runs) == 0 {
		t.Fatal("expected at least one run")
	}
	// entryCount lives at bytes 4..8 of the first record header.
	f, err := os.OpenFile(runs[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var huge [4]byte
	binary.LittleEndian.PutUint32(huge[:], 0xFFFFFFFF)
	if _, err := f.WriteAt(huge[:], 4); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := c.MergeIter(); err == nil {
		t.Fatal("expected corrupt entry count to fail fast, not attempt a 32GB allocation")
	}
}

func TestMergeIter_EmptyRunFileIsRejected(t *testing.T) {
	c, runs := spillTwoRuns(t)
	if err := os.Truncate(runs[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.MergeIter(); err == nil {
		t.Fatal("expected empty run file to be rejected")
	}
}

func TestMergeIter_PrefanInTruncationPropagates(t *testing.T) {
	// Force more than maxKeyedOpenRuns so MergeIter first collapses runs via
	// writeMergedRun; corrupt one input and assert the pre-merge fails.
	const n = maxKeyedOpenRuns + 2
	c := NewKeyedPostingCollector(1, t.TempDir())
	for i := 0; i < n; i++ {
		key := []byte{byte('a' + i)}
		if err := c.Add(key, []core.EntryID{core.EntryID(i)}); err != nil {
			t.Fatal(err)
		}
	}
	runs := c.runFiles
	if len(runs) != n {
		t.Fatalf("expected %d runs, got %d", n, len(runs))
	}
	// Corrupt the middle input of the first fan-in batch.
	victim := filepath.Join(t.TempDir(), "victim")
	data, err := os.ReadFile(runs[len(runs)/2])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, data[:len(data)-2], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(victim, runs[len(runs)/2]); err != nil {
		t.Fatal(err)
	}

	if _, err := c.MergeIter(); err == nil {
		t.Fatal("expected fan-in pre-merge to propagate the truncated run error")
	}
}
