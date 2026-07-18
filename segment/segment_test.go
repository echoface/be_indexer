package segment

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/echoface/be_indexer/core"
)

// makeE creates a properly encoded EntryID with the given K, document id, and include flag.
func makeE(k int, id core.DocID) core.EntryID {
	return core.NewEntryID(core.NewConjID(id, 0, k), true)
}

func TestBuilderReader(t *testing.T) {
	buf := new(bytes.Buffer)
	writer := NewInMemorySegmentBuilder(buf)

	metaAge := core.FieldMeta{
		ID:    1,
		Field: "age",
	}
	writer.AddField(metaAge)

	writer.AddPosting(1, "age", "18", []core.EntryID{makeE(1, 10), makeE(1, 20), makeE(1, 30)})
	writer.AddPosting(1, "age", "25", []core.EntryID{makeE(1, 15), makeE(1, 25)})

	if err := writer.Write(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	// Read existing term
	pi, err := reader.GetPostingsByTerm("age", "18")
	if err != nil {
		t.Fatal(err)
	}
	if pi.Current() != makeE(1, 10) {
		t.Fatalf("expected 10, got %d", pi.Current())
	}

	// Read another existing term
	pi2, err := reader.GetPostingsByTerm("age", "25")
	if err != nil {
		t.Fatal(err)
	}
	if pi2.Current() != makeE(1, 15) {
		t.Fatalf("expected 15, got %d", pi2.Current())
	}

	// Read missing term
	pi3, err := reader.GetPostingsByTerm("age", "100")
	if err != nil {
		t.Fatal(err)
	}
	if pi3 != nil {
		t.Fatalf("expected nil posting iterator, got %#v", pi3)
	}

	// Read missing field
	_, err = reader.GetPostingsByTerm("unknown", "18")
	if err != core.ErrUnknownQueryField {
		t.Fatalf("expected ErrUnknownQueryField, got %v", err)
	}
}

// TestBuilderRangeRoundTrip verifies ext_range blocks survive write/read and
// that Builder and ExternalBuilder produce byte-identical segments.
func TestBuilderRangeRoundTrip(t *testing.T) {
	field := core.FieldMeta{ID: 1, Field: "age", FieldOption: core.FieldOption{Container: core.IndexNameExtendRange}}
	type rng struct {
		lo, hi int64
		entry  core.EntryID
	}
	ranges := []rng{
		{18, 1 << 62, makeE(1, 100)},
		{-(1 << 62), 21, makeE(1, 200)},
		{18, 25, makeE(1, 300)},
		{30, 30, makeE(1, 400)},
	}
	build := func(external bool) []byte {
		buf := new(bytes.Buffer)
		var sink interface {
			SetDocCount(int)
			AddField(core.FieldMeta)
			AddRecord(field, container string, record any, entries []core.EntryID) error
			Write() error
		}
		if external {
			sink = NewExternalBuilder(buf, t.TempDir(), ExternalBuilderOptions{})
		} else {
			sink = NewInMemorySegmentBuilder(buf)
		}
		sink.SetDocCount(4)
		sink.AddField(field)
		for _, r := range ranges {
			if err := sink.AddRecord("age", "ext_range", core.RangeRecord{Lo: r.lo, Hi: r.hi}, []core.EntryID{r.entry}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sink.Write(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	want := build(false)
	got := build(true)
	if !bytes.Equal(want, got) {
		t.Fatalf("Builder and ExternalBuilder range bytes mismatch (%d vs %d)", len(want), len(got))
	}

	reader, err := NewSegmentReader(got)
	if err != nil {
		t.Fatal(err)
	}
	collect := func(q int64) []core.EntryID {
		iters, err := reader.GetRangePostings("age", q)
		if err != nil {
			t.Fatal(err)
		}
		var out []core.EntryID
		for _, it := range iters {
			for e := it.Current(); !e.IsNULLEntry(); e = it.SkipTo(e + 1) {
				out = append(out, e)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	cases := map[int64][]core.EntryID{
		17: {makeE(1, 200)},
		18: {makeE(1, 100), makeE(1, 200), makeE(1, 300)},
		25: {makeE(1, 100), makeE(1, 300)},
		30: {makeE(1, 100), makeE(1, 400)},
	}
	for q, want := range cases {
		got := collect(q)
		if len(got) != len(want) {
			t.Fatalf("q=%d got %v want %v", q, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("q=%d got %v want %v", q, got, want)
			}
		}
	}
}

func TestBuilderReaderSegmentV2MetadataWildcardsAndChecksum(t *testing.T) {
	buf := new(bytes.Buffer)
	wildcards := core.Entries{core.EntryID(30), core.EntryID(10)}
	writer := NewInMemorySegmentBuilderWithOptions(buf, InMemorySegmentBuilderOptions{
		SchemaHash: "sha256:schema",
		Wildcards:  wildcards,
	})
	writer.SetDocCount(1)
	writer.AddField(core.FieldMeta{ID: 1, Field: "age"})
	if err := writer.AddPosting(1, "age", "18", []core.EntryID{makeE(1, 20)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(); err != nil {
		t.Fatal(err)
	}

	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if reader.Version() != SegmentVersionV4 {
		t.Fatalf("version mismatch: %d", reader.Version())
	}
	if reader.SchemaHash() != "sha256:schema" {
		t.Fatalf("schema hash mismatch: %s", reader.SchemaHash())
	}
	gotWildcards := reader.Wildcards()
	if len(gotWildcards) != 2 || gotWildcards[0] != 10 || gotWildcards[1] != 30 {
		t.Fatalf("wildcards mismatch: %v", gotWildcards)
	}
	it, err := reader.GetPostingsByTerm("age", "18")
	if err != nil {
		t.Fatal(err)
	}
	if it.Current() != makeE(1, 20) {
		t.Fatalf("posting mismatch: %d", it.Current())
	}
}

func TestNewSegmentReaderRejectsSegmentV2BlockChecksumMismatch(t *testing.T) {
	buf := new(bytes.Buffer)
	writer := NewInMemorySegmentBuilderWithOptions(buf, InMemorySegmentBuilderOptions{Wildcards: core.Entries{10}})
	writer.SetDocCount(1)
	writer.AddField(core.FieldMeta{ID: 1, Field: "age"})
	if err := writer.AddPosting(1, "age", "18", []core.EntryID{makeE(1, 20)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(); err != nil {
		t.Fatal(err)
	}
	var meta MetaBlock
	metaOffset := binary.LittleEndian.Uint64(buf.Bytes()[len(buf.Bytes())-8:])
	if err := json.Unmarshal(buf.Bytes()[metaOffset:len(buf.Bytes())-8], &meta); err != nil {
		t.Fatal(err)
	}
	for _, blockName := range []string{plBlockName("age"), dictBlockName("age"), wildcardsBlockName} {
		t.Run(blockName, func(t *testing.T) {
			data := append([]byte(nil), buf.Bytes()...)
			block := meta.BlockIndex[blockName]
			data[block.Offset] ^= 0xff
			_, err := NewSegmentReader(data)
			if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Fatalf("expected checksum mismatch, got %v", err)
			}
		})
	}
}

func TestNewSegmentReaderRejectsUnsupportedSegmentVersion(t *testing.T) {
	buf := new(bytes.Buffer)
	writer := NewInMemorySegmentBuilder(buf)
	writer.SetDocCount(1)
	writer.AddField(core.FieldMeta{ID: 1, Field: "age"})
	if err := writer.AddPosting(1, "age", "18", []core.EntryID{makeE(1, 20)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(); err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), buf.Bytes()...)
	metaOffset := binary.LittleEndian.Uint64(data[len(data)-8:])
	var meta MetaBlock
	if err := json.Unmarshal(data[metaOffset:len(data)-8], &meta); err != nil {
		t.Fatal(err)
	}
	meta.Version = 1
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(metaBytes) != len(data[metaOffset:len(data)-8]) {
		t.Fatalf("metadata length changed: got=%d want=%d", len(metaBytes), len(data[metaOffset:len(data)-8]))
	}
	copy(data[metaOffset:len(data)-8], metaBytes)
	_, err = NewSegmentReader(data)
	if err == nil || !strings.Contains(err.Error(), "unsupported segment version 1") {
		t.Fatalf("expected unsupported segment version, got %v", err)
	}
}

func TestNewSegmentReaderRejectsSegmentV2ACChecksumMismatch(t *testing.T) {
	buf := new(bytes.Buffer)
	writer := NewInMemorySegmentBuilder(buf)
	writer.SetDocCount(1)
	writer.AddField(core.FieldMeta{ID: 1, Field: "keyword", FieldOption: core.FieldOption{Container: core.IndexNameACMatcher}})
	if err := writer.AddRecord("keyword", core.IndexNameACMatcher, "apple", []core.EntryID{makeE(1, 20)}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(); err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), buf.Bytes()...)
	metaOffset := binary.LittleEndian.Uint64(data[len(data)-8:])
	var meta MetaBlock
	if err := json.Unmarshal(data[metaOffset:len(data)-8], &meta); err != nil {
		t.Fatal(err)
	}
	block := meta.BlockIndex[containerBlockName("keyword", BlockKindAC)]
	data[block.Offset] ^= 0xff
	_, err := NewSegmentReader(data)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestNewSegmentReaderRejectsCorruptBlockBounds(t *testing.T) {
	tests := []struct {
		name    string
		block   BlockDef
		wantErr string
	}{
		{
			name:    "empty block",
			block:   BlockDef{Field: "age", Kind: BlockKindDict, Checksum: "sha256:bad", Offset: uint64(len(MagicNumber)), Size: 0},
			wantErr: "empty block",
		},
		{
			name:    "offset after metadata",
			block:   BlockDef{Field: "age", Kind: BlockKindDict, Checksum: "sha256:bad", Offset: uint64(len(MagicNumber)) + 2, Size: 1},
			wantErr: "exceeds metadata offset",
		},
		{
			name:    "size crosses metadata",
			block:   BlockDef{Field: "age", Kind: BlockKindDict, Checksum: "sha256:bad", Offset: 0, Size: uint64(len(MagicNumber)) + 1},
			wantErr: "exceeds data region",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := corruptSegmentWithBlock(tt.block)
			_, err := NewSegmentReader(bad)
			if err == nil {
				t.Fatal("expected corrupt block error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func corruptSegmentWithBlock(block BlockDef) []byte {
	metaOffset := uint64(len(MagicNumber))
	badMeta := MetaBlock{
		Version:  SegmentVersionV4,
		DocCount: 1,
		Fields:   []FieldMetaDump{{Name: "age", ID: 1}},
		BlockIndex: map[string]BlockDef{
			"k1_age_dict": block,
		},
	}
	metaBytes, err := json.Marshal(badMeta)
	if err != nil {
		panic(err)
	}
	bad := append([]byte{}, MagicNumber...)
	bad = append(bad, metaBytes...)
	footer := make([]byte, 8)
	binaryLittleEndianPutUint64(footer, metaOffset)
	bad = append(bad, footer...)
	return bad
}

func binaryLittleEndianPutUint64(b []byte, v uint64) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
	b[4] = byte(v >> 32)
	b[5] = byte(v >> 40)
	b[6] = byte(v >> 48)
	b[7] = byte(v >> 56)
}
