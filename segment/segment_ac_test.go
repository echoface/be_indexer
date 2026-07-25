package segment

import (
	"bytes"
	"github.com/echoface/be_indexer/core"
	"testing"
)

func TestSegmentACMatcher(t *testing.T) {
	buf := new(bytes.Buffer)
	writer := NewInMemorySegmentBuilder(buf)

	writer.AddField(core.FieldMeta{
		Field:       core.BEField("keyword"),
		FieldOption: core.FieldOption{Container: core.IndexNameACMatcher, Encoder: core.IndexNameACMatcher},
	})

	writer.AddRecord("keyword", core.IndexNameACMatcher, "apple", []core.EntryID{makeE(1, 1), makeE(1, 2)})
	writer.AddRecord("keyword", core.IndexNameACMatcher, "app", []core.EntryID{makeE(1, 3), makeE(1, 4)})
	writer.AddRecord("keyword", core.IndexNameACMatcher, "banana", []core.EntryID{makeE(1, 5)})
	writer.AddRecord("keyword", core.IndexNameACMatcher, "tree", []core.EntryID{makeE(1, 6)})

	err := writer.Write()
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	iterators, err := reader.MultiPatternSearch("keyword", "I love apple and banana")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Expecting iterators for "app", "apple", "banana"
	if len(iterators) != 3 {
		t.Errorf("Expected 3 iterators, got %d", len(iterators))
	}

	var matches []string
	for _, iter := range iterators {
		matches = append(matches, string(iter.Term().Value.(string)))
	}
	t.Logf("Matched terms: %v", matches)
}
