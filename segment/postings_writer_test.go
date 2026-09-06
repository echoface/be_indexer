package segment

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/echoface/be_indexer/core"
)

// bufferedOnlyWriter wraps a BlockWriter but deliberately does NOT expose
// OpenBlock, so WritePostings falls back to buffering the whole block and
// calling WriteBlock once. Delegating to a real segmentBlockWriter lets us
// compare the buffered fallback's framed output against the streaming path.
type bufferedOnlyWriter struct{ inner BlockWriter }

func (w bufferedOnlyWriter) WriteBlock(kind string, data []byte) error {
	return w.inner.WriteBlock(kind, data)
}

func feedPostings(c *KeyedPostingCollector) error {
	adds := []struct {
		key     string
		entries []core.EntryID
	}{
		{"aa", []core.EntryID{makeE(1, 30), makeE(1, 10)}},
		{"bb", []core.EntryID{makeE(2, 5)}},
		{"aa", []core.EntryID{makeE(1, 20)}}, // dup, merges
		{"cc", []core.EntryID{makeE(1, 1), makeE(1, 99), makeE(1, 50)}},
	}
	for _, a := range adds {
		if err := c.Add([]byte(a.key), a.entries); err != nil {
			return err
		}
	}
	return nil
}

type capturedRef struct {
	key string
	ref PostingRef
}

// runWritePostings drives WritePostings against a fresh segmentBlockWriter and
// returns the raw framed bytes, the block index, checksums, and the ref stream.
func runWritePostings(t *testing.T, bw BlockWriter, wrap func(BlockWriter) BlockWriter) ([]byte, map[string]BlockDef, map[string]string, []capturedRef) {
	t.Helper()
	buf := &bytes.Buffer{}
	var off uint64
	blockIndex := map[string]BlockDef{}
	checksums := map[string]string{}
	sbw := newSegmentBlockWriter("f", buf, &off, blockIndex, checksums)

	var target BlockWriter = sbw
	if wrap != nil {
		target = wrap(sbw)
	}

	c := NewKeyedPostingCollector(1<<20, "")
	if err := feedPostings(c); err != nil {
		t.Fatal(err)
	}
	var refs []capturedRef
	if _, err := c.WritePostings(target, func(key []byte, ref PostingRef) error {
		refs = append(refs, capturedRef{key: string(key), ref: ref})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), blockIndex, checksums, refs
}

// TestWritePostings_StreamVsBufferedByteEquivalence proves the streaming
// OpenBlock path and the buffered WriteBlock fallback emit byte-identical
// segment output, checksums, block index, and PostingRef streams.
func TestWritePostings_StreamVsBufferedByteEquivalence(t *testing.T) {
	streamBytes, streamIdx, streamSum, streamRefs := runWritePostings(t, nil, nil)
	bufBytes, bufIdx, bufSum, bufRefs := runWritePostings(t, nil, func(bw BlockWriter) BlockWriter {
		return bufferedOnlyWriter{inner: bw}
	})

	if !bytes.Equal(streamBytes, bufBytes) {
		t.Fatalf("segment bytes differ:\n stream=%x\n buffered=%x", streamBytes, bufBytes)
	}
	if !reflect.DeepEqual(streamIdx, bufIdx) {
		t.Fatalf("block index differs:\n stream=%v\n buffered=%v", streamIdx, bufIdx)
	}
	if !reflect.DeepEqual(streamSum, bufSum) {
		t.Fatalf("checksums differ:\n stream=%v\n buffered=%v", streamSum, bufSum)
	}
	if !reflect.DeepEqual(streamRefs, bufRefs) {
		t.Fatalf("ref stream differs:\n stream=%v\n buffered=%v", streamRefs, bufRefs)
	}

	// Sanity: exactly one postings block was registered and it is non-empty.
	def, ok := streamIdx["f_"+BlockKindPostings]
	if !ok {
		t.Fatalf("no postings block registered: %v", streamIdx)
	}
	if def.Size == 0 {
		t.Fatalf("postings block is empty")
	}
}

// TestWritePostings_RefsResolve confirms each returned PostingRef resolves to a
// correctly ordered posting list inside the produced block.
func TestWritePostings_RefsResolve(t *testing.T) {
	streamBytes, idx, _, refs := runWritePostings(t, nil, nil)
	def := idx["f_"+BlockKindPostings]
	block := streamBytes[def.Offset : def.Offset+def.Size]

	if len(refs) != 3 { // aa, bb, cc
		t.Fatalf("expected 3 refs, got %d: %v", len(refs), refs)
	}
	// Refs arrive in ascending key order.
	wantKeys := []string{"aa", "bb", "cc"}
	for i, r := range refs {
		if r.key != wantKeys[i] {
			t.Fatalf("ref %d key = %q, want %q", i, r.key, wantKeys[i])
		}
		pl, err := NewPostingListAt(block, r.ref)
		if err != nil {
			t.Fatalf("resolve %q: %v", r.key, err)
		}
		// Entries must be ascending (PostingIterator contract).
		var prev core.EntryID
		for j := 0; j < pl.Count(); j++ {
			e := pl.EntryIndex(uint32(j))
			if j > 0 && e < prev {
				t.Fatalf("term %q entries not ascending at %d: %d < %d", r.key, j, e, prev)
			}
			prev = e
		}
	}
}

// TestWritePostings_EmptyWritesNothing verifies an empty collector opens no
// block (the reader rejects zero-length postings blocks).
func TestWritePostings_EmptyWritesNothing(t *testing.T) {
	buf := &bytes.Buffer{}
	var off uint64
	blockIndex := map[string]BlockDef{}
	checksums := map[string]string{}
	sbw := newSegmentBlockWriter("f", buf, &off, blockIndex, checksums)

	c := NewKeyedPostingCollector(1<<20, "")
	n, err := c.WritePostings(sbw, func(key []byte, ref PostingRef) error {
		t.Fatalf("onRef should not be called for empty collector")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0 terms, got %d", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no bytes written, got %d", buf.Len())
	}
	if len(blockIndex) != 0 {
		t.Fatalf("expected no block registered, got %v", blockIndex)
	}
}
