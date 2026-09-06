package segment

import (
	"sort"

	"github.com/echoface/be_indexer/core"
)

// WritePostings drains the collector and writes a single FlatPostingList
// (BlockKindPostings) block, invoking onRef once per distinct term with the
// term's block-relative PostingRef. It returns the number of terms written.
//
// It is a method on *KeyedPostingCollector because the collector is the sole
// owner of the work: bw is a pure output sink and onRef a per-term callback.
//
// Memory: postings are streamed straight into the segment when bw implements
// StreamBlockWriter, so the whole EntryID set (E) never resides in memory at
// once — only one term's posting list is serialized at a time. If bw is a plain
// BlockWriter (e.g. a test double), the block is buffered and written in one
// WriteBlock call as a fallback. The dictionary structure a caller builds from
// the onRef stream is O(distinct terms), the irreducible cost.
//
// onRef receives the term key (valid only for the duration of the call — copy
// it if retained) in ascending key order, matching MergeIter's ordering. No
// block is opened when the collector is empty, so no zero-length postings block
// is ever written (which the reader would reject).
func (c *KeyedPostingCollector) WritePostings(bw BlockWriter, onRef func(key []byte, ref PostingRef) error) (int, error) {
	it, err := c.MergeIter()
	if err != nil {
		return 0, err
	}
	defer it.Close()

	pw := &postingBlockWriter{bw: bw}
	n := 0
	for it.Next() {
		rec := it.Record()
		// The reader binary-searches posting lists and requires ascending EntryID
		// order (PostingIterator contract).
		sort.Slice(rec.Entries, func(i, j int) bool { return rec.Entries[i] < rec.Entries[j] })
		ref, err := pw.add(rec.Entries)
		if err != nil {
			return n, err
		}
		if onRef != nil {
			if err := onRef(rec.Key, ref); err != nil {
				return n, err
			}
		}
		n++
	}
	if err := it.Err(); err != nil {
		return n, err
	}
	if err := pw.close(); err != nil {
		return n, err
	}
	return n, nil
}

// postingBlockWriter lazily opens the postings block on the first term and
// serializes one posting list at a time. It streams via StreamBlockWriter when
// available, otherwise buffers and flushes in a single WriteBlock at close.
type postingBlockWriter struct {
	bw     BlockWriter
	stream BlockStream // set when bw is a StreamBlockWriter
	buf    []byte      // buffered fallback (bw is a plain BlockWriter)
	relOff uint64      // running block-relative byte offset of the next list
	opened bool
}

// add serializes one posting list, returns its block-relative PostingRef, and
// advances the running offset.
func (p *postingBlockWriter) add(entries []core.EntryID) (PostingRef, error) {
	if !p.opened {
		if sbw, ok := p.bw.(StreamBlockWriter); ok {
			s, err := sbw.OpenBlock(BlockKindPostings)
			if err != nil {
				return PostingRef{}, err
			}
			p.stream = s
		}
		p.opened = true
	}
	ref := PostingRef{Offset: p.relOff, Count: uint32(len(entries))}
	// WriteFlatPostingList allocates O(one posting list) — the accepted, per-term
	// serialization cost — never the whole field at once. It also validates the
	// entry count against the uint32 format limit, returning an error instead of
	// panicking on an oversized posting list.
	b, err := WriteFlatPostingList(entries)
	if err != nil {
		return PostingRef{}, err
	}
	if p.stream != nil {
		if _, err := p.stream.Write(b); err != nil {
			return PostingRef{}, err
		}
	} else {
		p.buf = append(p.buf, b...)
	}
	p.relOff += uint64(len(b))
	return ref, nil
}

// close finalizes the postings block. It is a no-op when no term was added.
func (p *postingBlockWriter) close() error {
	if !p.opened {
		return nil
	}
	if p.stream != nil {
		return p.stream.Close()
	}
	return p.bw.WriteBlock(BlockKindPostings, p.buf)
}
