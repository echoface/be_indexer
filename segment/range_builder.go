package segment

import (
	"encoding/binary"
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// RangeBuilder accumulates interval entries and builds a RangeIndex segment
// block at Build time.
//
// Like the dict/mph/ac builders, it funnels every (interval, EntryID) pair
// through a KeyedPostingCollector so that accumulation past
// BuilderEnv.MaxPostingsInMemory spills sorted runs to disk instead of holding
// the whole field in RAM. This keeps peak build memory bounded even for numeric
// fields with tens of millions of documents. The interval [lo, hi] is used as
// the collector key, so the collector's k-way merge naturally groups every
// EntryID sharing the same interval — replacing the former in-memory dedup map.
type RangeBuilder struct {
	collector *KeyedPostingCollector
}

// NewRangeBuilder creates a fresh RangeBuilder honoring the spill environment.
func NewRangeBuilder(env BuilderEnv) IndexBuilder {
	return &RangeBuilder{
		collector: NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
}

// AddRecord receives a record and its associated EntryIDs.
func (b *RangeBuilder) AddRecord(record any, entries []core.EntryID) error {
	rr, ok := record.(core.RangeRecord)
	if !ok {
		return fmt.Errorf("ext_range: expected core.RangeRecord, got %T", record)
	}
	if rr.Lo > rr.Hi {
		return fmt.Errorf("ext_range: invalid interval [%d,%d]", rr.Lo, rr.Hi)
	}
	key := encodeIntervalKey(rr.Lo, rr.Hi)
	return b.collector.Add(key, entries)
}

// Build merges accumulated intervals (from memory and any spilled runs) and
// writes a RangeIndex block. It streams merged records one at a time via
// MergeIter so the full EntryID set is not duplicated; the []Interval and the
// range index built from it remain O(E) by nature of the interval structure.
func (b *RangeBuilder) Build(bw BlockWriter) error {
	it, err := b.collector.MergeIter()
	if err != nil {
		return err
	}
	defer it.Close()
	var intervals []Interval
	for it.Next() {
		rec := it.Record()
		lo, hi := decodeIntervalKey(rec.Key)
		for _, eid := range rec.Entries {
			intervals = append(intervals, Interval{Lo: lo, Hi: hi, Entry: eid})
		}
	}
	if err := it.Err(); err != nil {
		return err
	}
	if len(intervals) == 0 {
		return nil
	}
	data, err := BuildRangeIndex(intervals)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return bw.WriteBlock(core.IndexNameExtendRange, data)
}

// encodeIntervalKey packs [lo, hi] into a fixed 16-byte collector key. Each
// int64 is stored big-endian with its sign bit flipped so that bytewise
// comparison matches signed-integer ordering; this keeps spilled runs sorted in
// a stable, meaningful order and lets the collector group identical intervals.
func encodeIntervalKey(lo, hi int64) []byte {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], uint64(lo)^(1<<63))
	binary.BigEndian.PutUint64(buf[8:16], uint64(hi)^(1<<63))
	return buf[:]
}

// decodeIntervalKey reverses encodeIntervalKey.
func decodeIntervalKey(k []byte) (lo, hi int64) {
	lo = int64(binary.BigEndian.Uint64(k[0:8]) ^ (1 << 63))
	hi = int64(binary.BigEndian.Uint64(k[8:16]) ^ (1 << 63))
	return lo, hi
}
