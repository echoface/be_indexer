package storage

import (
	"bytes"
	"reflect"
	"testing"
)

func TestPostingRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ids  []uint64
	}{
		{name: "sparse", ids: []uint64{1, 1000, 1000000}},
		{name: "runs", ids: []uint64{10, 11, 12, 13, 14, 100, 101, 102}},
		{name: "raw-preferred", ids: []uint64{1, 1 << 32, 2 << 32, 3 << 32}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoding, data, err := encodePosting(tc.ids)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodePosting(encoding, data, uint32(len(tc.ids)))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.ids) {
				t.Fatalf("encoding=%s got %v want %v", encoding, got, tc.ids)
			}
		})
	}
}

func TestSegmentDictionaryPostingAndInspector(t *testing.T) {
	builder := NewSegmentBuilder()
	if err := builder.AddPosting("city", "beijing", []uint64{1, 2, 3, 3}); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddPosting("city", "shanghai", []uint64{2, 8}); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddPosting("age", "18", []uint64{1, 4}); err != nil {
		t.Fatal(err)
	}
	segment, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	if got := segment.Fields(); !reflect.DeepEqual(got, []string{"age", "city"}) {
		t.Fatalf("fields=%v", got)
	}
	if segment.TermCount() != 3 {
		t.Fatalf("term count=%d want 3", segment.TermCount())
	}
	termID, ok := segment.LookupTerm("city", "beijing")
	if !ok {
		t.Fatal("term not found")
	}
	if got, err := segment.Posting(termID); err != nil || !reflect.DeepEqual(got, []uint64{1, 2, 3}) {
		t.Fatalf("posting=%v err=%v", got, err)
	}
	info, err := segment.InspectTerm("city", "beijing")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != termID || info.Count != 3 || info.Bytes == 0 {
		t.Fatalf("unexpected term info: %+v", info)
	}
	sample, err := segment.SamplePosting("city", "beijing", 2)
	if err != nil || !reflect.DeepEqual(sample, []uint64{1, 2}) {
		t.Fatalf("sample=%v err=%v", sample, err)
	}
	if _, err := segment.SamplePosting("city", "beijing", -1); err == nil {
		t.Fatal("negative sample limit should fail")
	}
	report := segment.MemoryReport()
	if report.TotalBytes <= 0 || report.PostingBytes <= 0 || report.TermCount != 3 {
		t.Fatalf("unexpected memory report: %+v", report)
	}
}

func TestSegmentMarshalLoadAndChecksum(t *testing.T) {
	builder := NewSegmentBuilder()
	if err := builder.AddLiveDoc(9); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddPosting("field", "value", []uint64{1, 2, 100}); err != nil {
		t.Fatal(err)
	}
	original, err := builder.Seal()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := original.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSegment(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := loaded.PostingFor("field", "value")
	if err != nil || !reflect.DeepEqual(got, []uint64{1, 2, 100}) {
		t.Fatalf("loaded posting=%v err=%v", got, err)
	}
	live, err := loaded.AllDocIDs()
	if err != nil || !reflect.DeepEqual(live, []uint64{1, 2, 9, 100}) {
		t.Fatalf("loaded live docs=%v err=%v", live, err)
	}
	if !bytes.Equal(encoded, mustMarshal(t, loaded)) {
		t.Fatal("marshal after load is not deterministic")
	}

	corrupt := append([]byte(nil), encoded...)
	corrupt[len(corrupt)-5] ^= 0xff
	if _, err := LoadSegment(corrupt); err == nil {
		t.Fatal("corrupted segment should fail checksum validation")
	}
}

func TestSegmentRejectsInvalidInput(t *testing.T) {
	builder := NewSegmentBuilder()
	if err := builder.AddPosting("field", "value", []uint64{2, 1}); err == nil {
		t.Fatal("unsorted posting should be rejected")
	}
	if err := builder.AddPosting("", "value", []uint64{1}); err == nil {
		t.Fatal("empty field should be rejected")
	}
	if _, err := LoadSegment([]byte("not a segment")); err == nil {
		t.Fatal("invalid header should be rejected")
	}
}

func mustMarshal(t *testing.T, segment *Segment) []byte {
	t.Helper()
	data, err := segment.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
