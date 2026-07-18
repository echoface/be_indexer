package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// RangeBuilder accumulates interval entries and builds a RangeIndex segment tree
// block at Build time. It implements ContainerBuilder for the "ext_range" container.
type RangeBuilder struct {
	intervals []intervalEntry
}

type intervalEntry struct {
	lo, hi  int64
	entries []core.EntryID
}

// NewRangeBuilder creates a fresh RangeBuilder.
func NewRangeBuilder() *RangeBuilder {
	return &RangeBuilder{}
}

// AddPosting receives a record and its associated EntryIDs. The record is
// expected to be a core.RangeRecord.
func (b *RangeBuilder) AddPosting(record any, entries []core.EntryID) error {
	rr, ok := record.(core.RangeRecord)
	if !ok {
		return fmt.Errorf("ext_range: expected core.RangeRecord, got %T", record)
	}
	b.intervals = append(b.intervals, intervalEntry{lo: rr.Lo, hi: rr.Hi, entries: append([]core.EntryID(nil), entries...)})
	return nil
}

// Build merges accumulated intervals and serializes a RangeIndex byte block.
func (b *RangeBuilder) Build() ([]byte, error) {
	merged := mergeIntervalEntries(b.intervals)
	intervals := make([]Interval, 0, len(merged))
	for _, e := range merged {
		for _, eid := range e.entries {
			intervals = append(intervals, Interval{Lo: e.lo, Hi: e.hi, Entry: eid})
		}
	}
	return BuildRangeIndex(intervals)
}

// mergeIntervalEntries groups intervalEntry by (lo, hi) and merges their entry lists.
func mergeIntervalEntries(entries []intervalEntry) []intervalEntry {
	if len(entries) <= 1 {
		return entries
	}
	type key struct{ lo, hi int64 }
	m := make(map[key]*intervalEntry)
	for i := range entries {
		e := &entries[i]
		k := key{e.lo, e.hi}
		if exist, ok := m[k]; ok {
			exist.entries = append(exist.entries, e.entries...)
		} else {
			m[k] = e
		}
	}
	out := make([]intervalEntry, 0, len(m))
	for _, e := range m {
		out = append(out, *e)
	}
	return out
}
