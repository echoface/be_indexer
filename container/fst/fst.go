// Package fst provides a Finite State Transducer (FST) dictionary container
// that replaces FlatDict binary search with an ordered, prefix-compressed term
// dictionary for exact-term fields.
//
// Users opt in per-field via FieldOption{IndexType: "fst_dict"}.
// No custom encoder is needed — the default ExactTermEncoder handles value
// encoding, and the engine routes queries through the fst container when the
// field's IndexType is "fst_dict".
//
// When to choose fst_dict over the alternatives:
//
//   - default (FlatDict): small/medium vocabularies. O(log n) binary search over
//     a sorted term array. Smallest build cost, no external dependency.
//   - mph_dict: large vocabularies of opaque, high-entropy keys (ids, hashes).
//     O(1) lookup, but the term strings are not stored so it cannot enumerate.
//   - fst_dict: large vocabularies with shared prefixes/suffixes (URLs, package
//     names, tokenized text). The FST folds common affixes into a shared graph,
//     giving strong on-disk compression that neither FlatDict nor mph provide.
//     Lookup is O(len(term)) and independent of vocabulary size.
//
// The FST maps each term to the byte offset of its posting list inside the
// field's postings block. The posting count is recovered from the
// FlatPostingList header, so — unlike mph — the value carries no PostingRef
// payload: a single uint64 offset per term. The FST replaces the FlatDict block
// entirely for opted-in fields, so only the postings block and the FST block are
// written.
//
// The serialized FST is loaded zero-copy via vellum.Load over the mapped block
// bytes. vellum.Get walks the transducer for O(len(term)) lookups.
package fst

import (
	"github.com/echoface/be_indexer/segment"
)

// IndexName is the value used in FieldOption.IndexType to select this container.
const IndexName = "fst_dict"

func init() {
	segment.RegisterIndex(IndexName, segment.IndexDef{
		Reader:  newReaderFactory,
		Builder: newBuilderFactory,
	})
}
