package fst

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/blevesearch/vellum"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newBuilderFactory(env segment.BuilderEnv) segment.IndexBuilder {
	return NewFSTBuilder(env)
}

// FSTBuilder accumulates term → EntryID mappings and, at Build time, writes a
// FlatPostingList block plus a vellum FST block that maps each term to the byte
// offset of its posting list within that block.
//
// Like the dict/mph/ac builders, every (term, entries) pair is funneled through
// a KeyedPostingCollector so accumulation past BuilderEnv.MaxPostingsInMemory
// spills sorted runs to disk instead of holding the whole field in RAM. The
// collector's k-way merge also yields records in strict lexicographic key order,
// which is exactly the order vellum.Builder.Insert requires.
type FSTBuilder struct {
	collector *segment.KeyedPostingCollector
	// terms is the advanced/test path: callers may push (term, offset) pairs
	// directly against a pre-written posting block instead of going through the
	// collector. Kept sorted-on-Build like the mph builder's equivalent path.
	terms []termOffset
}

type termOffset struct {
	term   string
	offset uint64
}

// NewFSTBuilder creates a fresh FSTBuilder honoring the spill environment.
func NewFSTBuilder(env segment.BuilderEnv) *FSTBuilder {
	return &FSTBuilder{
		collector: segment.NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
}

// AddRecord accumulates a term → entries mapping via the collector. The record
// must be a string or []byte term (produced by the default ExactTermEncoder).
func (b *FSTBuilder) AddRecord(record any, entries []core.EntryID) error {
	term, ok := record.(string)
	if !ok {
		termBytes, ok2 := record.([]byte)
		if !ok2 {
			return fmt.Errorf("FSTBuilder: expected string or []byte record, got %T", record)
		}
		return b.collector.Add(termBytes, entries)
	}
	return b.collector.Add([]byte(term), entries)
}

// AddPosting registers a term whose posting list already lives at the given
// block-relative offset. This is the direct path used by tests and advanced
// callers that write the posting block themselves; it bypasses the collector.
func (b *FSTBuilder) AddPosting(term string, ref segment.PostingRef) {
	b.terms = append(b.terms, termOffset{term: term, offset: ref.Offset})
}

// Build writes the postings block followed by the FST block. The FST maps every
// term to the byte offset of its posting list header inside the postings block;
// the posting count is recovered by the reader from the FlatPostingList header,
// so the FST value is a single uint64 offset with no PostingRef payload.
func (b *FSTBuilder) Build(bw segment.BlockWriter) error {
	if b.collector != nil {
		_, err := segment.BuildPostings(b.collector, bw, func(key []byte, ref segment.PostingRef) error {
			b.terms = append(b.terms, termOffset{term: string(key), offset: ref.Offset})
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(b.terms) == 0 {
		return nil
	}

	// vellum.Insert requires strictly ascending keys. Collector output is already
	// sorted, but the direct AddPosting path may not be, so sort defensively.
	sort.Slice(b.terms, func(i, j int) bool { return b.terms[i].term < b.terms[j].term })

	var buf bytes.Buffer
	builder, err := vellum.New(&buf, nil)
	if err != nil {
		return fmt.Errorf("fst: new builder: %w", err)
	}
	var prev string
	for i, t := range b.terms {
		if i > 0 && t.term == prev {
			return fmt.Errorf("fst: duplicate term %q", t.term)
		}
		if err := builder.Insert([]byte(t.term), t.offset); err != nil {
			return fmt.Errorf("fst: insert %q: %w", t.term, err)
		}
		prev = t.term
	}
	if err := builder.Close(); err != nil {
		return fmt.Errorf("fst: finalize: %w", err)
	}
	return bw.WriteBlock(IndexName, buf.Bytes())
}
