package segment

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"
	"unsafe"

	"github.com/echoface/be_indexer/core"
	. "github.com/smartystreets/goconvey/convey"
)

func TestLittleEndianViews(t *testing.T) {
	Convey("aligned and unaligned integer views preserve the file's byte order", t, func() {
		for _, forcePortable := range []bool{false, true} {
			for _, offset := range []int{0, 1} {
				storage := make([]uint64, 4)
				backing := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), 32)
				raw := backing[offset : offset+16]
				binary.LittleEndian.PutUint64(raw[:8], 0x0102030405060708)
				binary.LittleEndian.PutUint64(raw[8:], 0x8070605040302010)
				little := nativeLittleEndian && !forcePortable
				entries := mapLittleEndian[core.EntryID](raw, 2, little)
				So(entries, ShouldResemble, []core.EntryID{0x0102030405060708, 0x8070605040302010})
				words := mapLittleEndian[uint32](raw, 4, little)
				So(words, ShouldResemble, []uint32{0x05060708, 0x01020304, 0x40302010, 0x80706050})
				So(unsafe.Pointer(&entries[0]) == unsafe.Pointer(&raw[0]), ShouldEqual, little && offset == 0)
				So(unsafe.Pointer(&words[0]) == unsafe.Pointer(&raw[0]), ShouldEqual, little && offset == 0)
			}
		}
		So(mapEntryIDs(nil, 0), ShouldBeNil)
		So(mapUint32s(nil, 0), ShouldBeNil)
	})
}

func TestRangeReaderStructuralValidation(t *testing.T) {
	Convey("malformed ranges are rejected before any query", t, func() {
		cases := map[string][]byte{
			"cycle":                 encodeRangeIndex([]nodeRecord{{left: 0, right: 0}}, nil, nil, nil),
			"out of bounds child":   encodeRangeIndex([]nodeRecord{{left: 1, right: 2}}, nil, nil, nil),
			"one missing child":     encodeRangeIndex([]nodeRecord{{left: -1, right: 0}}, nil, nil, nil),
			"invalid leaf marker":   encodeRangeIndex([]nodeRecord{{left: -2, right: -2}}, nil, nil, nil),
			"unreachable node":      encodeRangeIndex([]nodeRecord{{left: -1, right: -1}, {left: -1, right: -1}}, nil, nil, nil),
			"bad posting":           encodeRangeIndex([]nodeRecord{{left: -1, right: -1, postOff: 9}}, nil, nil, nil),
			"point without posting": encodeRangeIndex(nil, []int64{1}, []uint32{0}, nil),
			"point offset overflow": encodeRangeIndex(nil, []int64{1}, []uint32{math.MaxUint32}, nil),
		}
		pl, err := EncodeFlatPostingList([]core.EntryID{mkE(1, 1)})
		So(err, ShouldBeNil)
		cases["unsorted points"] = encodeRangeIndex(nil, []int64{2, 1}, []uint32{1, 1}, pl)
		cases["duplicate points"] = encodeRangeIndex(nil, []int64{1, 1}, []uint32{1, 1}, pl)
		cases["unaligned posting"] = encodeRangeIndex(nil, []int64{1}, []uint32{2}, pl)
		cases["truncated posting"] = encodeRangeIndex(nil, []int64{1}, []uint32{1}, pl[:8])
		oversize := encodeRangeIndex(nil, nil, nil, nil)
		binary.LittleEndian.PutUint32(oversize[8:12], math.MaxUint32)
		cases["overflowing node count"] = oversize
		for name, data := range cases {
			Convey(name, func() {
				_, err := NewRangeReader(data)
				So(err, ShouldNotBeNil)
			})
		}
	})
	Convey("unbounded ranges have no duplicate MinInt64 boundary", t, func() {
		data, err := BuildRangeIndex([]Interval{{Lo: math.MinInt64, Hi: math.MaxInt64, Entry: mkE(1, 7)}})
		So(err, ShouldBeNil)
		ri, err := NewRangeReader(data)
		So(err, ShouldBeNil)
		for _, q := range []int64{math.MinInt64, 0, math.MaxInt64} {
			iters, err := ri.Stab("a", q)
			So(err, ShouldBeNil)
			So(len(iters), ShouldEqual, 1)
			So(iters[0].Current(), ShouldEqual, mkE(1, 7))
		}
	})
}

func TestPostingAndDictionaryValidation(t *testing.T) {
	Convey("posting count arithmetic and reference metadata are checked", t, func() {
		data := make([]byte, 8)
		binary.LittleEndian.PutUint32(data, 1<<29) // wraps an int byte length on 386
		_, err := NewFlatPostingList(data)
		So(err, ShouldNotBeNil)
		data, err = EncodeFlatPostingList([]core.EntryID{1})
		So(err, ShouldBeNil)
		for _, ref := range []PostingRef{{Offset: math.MaxUint64, Count: 1}, {Count: 2}, {Count: 0}} {
			_, err := NewPostingListAt(data, ref)
			So(err, ShouldNotBeNil)
		}
	})
	Convey("dictionary offsets and lexical order are validated on open", t, func() {
		data, err := EncodeFlatDict(map[string]PostingRef{"a": {Count: 1}, "b": {Count: 1}})
		So(err, ShouldBeNil)
		for _, mutate := range []func([]byte){
			func(b []byte) { binary.LittleEndian.PutUint32(b[:4], 1<<28) },
			func(b []byte) { binary.LittleEndian.PutUint32(b[4:8], 0) },
			func(b []byte) { binary.LittleEndian.PutUint32(b[8:12], math.MaxUint32) },
			func(b []byte) { binary.LittleEndian.PutUint32(b[24:28], 8) },
			func(b []byte) { b[len(b)-1] = 'a' },
		} {
			bad := append([]byte(nil), data...)
			mutate(bad)
			_, err := NewFlatDict(bad)
			So(err, ShouldNotBeNil)
		}
	})
}

func TestSegmentCrossBlockValidation(t *testing.T) {
	Convey("valid checksums do not hide invalid posting references", t, func() {
		for _, kind := range []string{core.IndexNameDefault, core.IndexNameACMatcher} {
			buf := new(bytes.Buffer)
			b := NewInMemorySegmentBuilder(buf)
			So(b.AddField("a", core.FieldOption{IndexType: kind}), ShouldBeNil)
			So(b.AddRecord("a", "term", []core.EntryID{mkE(1, 1)}), ShouldBeNil)
			So(b.Write(), ShouldBeNil)
			data := buf.Bytes()
			off := binary.LittleEndian.Uint64(data[len(data)-8:])
			var meta MetaBlock
			So(json.Unmarshal(data[off:len(data)-8], &meta), ShouldBeNil)
			delete(meta.BlockIndex, plBlockName("a"))
			metadata, err := json.Marshal(meta)
			So(err, ShouldBeNil)
			bad := append(append([]byte(nil), data[:off]...), metadata...)
			bad = binary.LittleEndian.AppendUint64(bad, off)
			for _, mode := range []BlockChecksumMode{BlockChecksumOnOpen, BlockChecksumDisabled} {
				_, err := NewSegmentReaderWithOptions(bad, ReaderOptions{BlockChecksumMode: mode})
				So(err, ShouldNotBeNil)
			}
		}
	})
}

func FuzzRangeReader(f *testing.F) {
	seed, err := BuildRangeIndex([]Interval{{Lo: -10, Hi: 10, Entry: mkE(1, 1)}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed, int64(0))
	f.Add(encodeRangeIndex([]nodeRecord{{left: 0, right: 0}}, nil, nil, nil), int64(0))
	f.Fuzz(func(t *testing.T, data []byte, q int64) {
		if len(data) > 1<<20 {
			return
		}
		ri, err := NewRangeReader(data)
		if err != nil {
			return
		}
		iters, err := ri.Stab("a", q)
		if err != nil {
			t.Fatalf("accepted reader failed query: %v", err)
		}
		for _, it := range iters {
			it.SkipTo(core.EndEntryID)
		}
	})
}
