package segment

import (
	"sort"
)

// DictPostingsWriter writes FlatDict and FlatPostingList blocks for sorted
// keyed records. It returns the PostingRef map (term → PostingRef) so
// containers that need to reference posting lists (e.g. AC, hybrid) can store
// the refs in their own data structures.
type DictPostingsWriter struct{}

// Write writes posting and dict blocks via bw. Sorted records must be in
// strict lexicographic key order.
func (w *DictPostingsWriter) Write(
	sorted []KeyedRecord,
	bw BlockWriter,
) (map[string]PostingRef, error) {
	if len(sorted) == 0 {
		return nil, nil
	}

	var plBuf []byte
	refs := make(map[string]PostingRef, len(sorted))
	dict := make(map[string]PostingRef, len(sorted))

	for _, rec := range sorted {
		// Sort entries within the posting list: the reader binary searches
		// against posting lists and requires ascending EntryID order.
		sort.Slice(rec.Entries, func(i, j int) bool { return rec.Entries[i] < rec.Entries[j] })
		ref := PostingRef{Offset: uint64(len(plBuf)), Count: uint32(len(rec.Entries))}
		plBuf = append(plBuf, WriteFlatPostingList(rec.Entries)...)
		refs[string(rec.Key)] = ref
		dict[string(rec.Key)] = ref
	}

	if err := bw.WriteBlock(BlockKindPostings, plBuf); err != nil {
		return nil, err
	}
	if err := bw.WriteBlock(BlockKindDict, WriteFlatDict(dict)); err != nil {
		return nil, err
	}
	return refs, nil
}
