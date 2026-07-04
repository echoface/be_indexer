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
		FieldOption: core.FieldOption{Container: core.IndexNameACMatcher},
	})

	writer.AddPosting(1, "keyword", "apple", []core.EntryID{1, 2})
	writer.AddPosting(1, "keyword", "app", []core.EntryID{3, 4})
	writer.AddPosting(1, "keyword", "banana", []core.EntryID{5})
	writer.AddPosting(1, "keyword", "tree", []core.EntryID{6})

	err := writer.Write()
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	reader, err := NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	iterators, err := reader.MultiPatternSearch(1, "keyword", "I love apple and banana")
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
