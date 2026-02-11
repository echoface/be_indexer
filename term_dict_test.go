package be_indexer

import (
	"bytes"
	"sort"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestTermDictionary(t *testing.T) {
	Convey("Test core.TermDictionary", t, func() {
		// 1. Prepare some terms
		terms := []KVTerm{
			{FieldID: 1, Value: "apple"},
			{FieldID: 1, Value: "banana"},
			{FieldID: 2, Value: "cat"},
			{FieldID: 2, Value: "dog"},
		}
		// Sort them as required by Builder
		sort.Slice(terms, func(i, j int) bool {
			if terms[i].FieldID != terms[j].FieldID {
				return terms[i].FieldID < terms[j].FieldID
			}
			return terms[i].Value < terms[j].Value
		})

		Convey("Test VellumTermDictBuilder and VellumTermDict", func() {
			builder := NewVellumTermDictBuilder()
			for _, term := range terms {
				err := builder.Add(term)
				So(err, ShouldBeNil)
			}
			protoDict, err := builder.Build()
			So(err, ShouldBeNil)
			So(protoDict, ShouldNotBeNil)
			
			// Check proto content size roughly
			So(len(protoDict.FstData), ShouldBeGreaterThan, 0)
			
			// Load into TermDict (Reader mode)
			dict := NewTermDictFromProto(protoDict)
			// Should be Vellum
			_, ok := dict.(*VellumTermDict)
			So(ok, ShouldBeTrue)

			So(dict.Size(), ShouldEqual, len(terms))

			// Test Get
			id, found := dict.Get(1, "apple")
			So(found, ShouldBeTrue)
			So(id, ShouldEqual, 0)

			id, found = dict.Get(2, "dog")
			So(found, ShouldBeTrue)
			So(id, ShouldEqual, 3)

			id, found = dict.Get(1, "orange")
			So(found, ShouldBeFalse)

			// Test Decode
			fid, val, found := dict.Decode(1)
			So(found, ShouldBeTrue)
			So(fid, ShouldEqual, 1)
			So(val, ShouldEqual, "banana")
		})
	})
}

func TestMemSegmentBuilder_Flush_Phase2(t *testing.T) {
	Convey("Test MemSegmentBuilder produces Phase 2 format", t, func() {
		builder := NewMemSegmentBuilder()
		
		// Add some documents
		// Use AddEntryWithFieldID for low-level testing
		
		// key1: Field 1, Value "test1", core.EntryID 100
		builder.AddEntryWithFieldID(1, "test1", 100)
		
		// key2: Field 2, Value "test2", core.EntryID 200
		builder.AddEntryWithFieldID(2, "test2", 200)

		// Flush to buffer
		var buf bytes.Buffer
		err := builder.Flush(&buf)
		So(err, ShouldBeNil)
		data := buf.Bytes()
		So(len(data), ShouldBeGreaterThan, 0)

		// Verify it can be loaded by BlockSegmentReader as Phase 2
		reader, err := NewBlockSegmentReader(data)
		So(err, ShouldBeNil)
		So(reader, ShouldNotBeNil)
		
		// Verify internal state of reader
		// It should use dict, not fail back to flat map
		
		// Test retrieval
		// Note: GetPostingsWithFieldID takes raw value. 
		// "test1" will be tokenized by DefaultTokenizer -> ["test1"]
		iter, err := reader.GetPostingsWithFieldID(1, "field1", "test1")
		So(err, ShouldBeNil)
		// Iter might be nil if not found, but we expect it to be found
		So(iter, ShouldNotBeNil) 
		
		// Since we can't easily iterate the iter without more setup (it's internal),
		// we just ensure we got a valid iterator back.
	})
}
