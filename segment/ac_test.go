package segment

import (
	"reflect"
	"sort"
	"testing"
)

func TestACBuilderAndReader(t *testing.T) {
	// Words to build into the AC Automaton
	words := []string{
		"apple",
		"app",
		"banana",
		"appletree",
		"tree",
		"x",
	}

	builder := NewACBuilder(BuilderEnv{})
	// Adding works best if sorted (simplifies the Trie), but should work anyway
	sort.Strings(words)
	// Assign each word a recognizable PostingRef (Offset = index+1) and remember
	// the reverse mapping so the test can assert on matched words.
	byRef := make(map[uint64]string, len(words))
	for i, w := range words {
		ref := PostingRef{Offset: uint64(i + 1), Count: 1}
		builder.AddPosting(w, ref)
		byRef[ref.Offset] = w
	}

	// Compile the automaton into the binary mmap format
	bin, err := builder.Compile()
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	// Read the binary format using zero-copy reader
	reader, err := NewACReader(bin)
	if err != nil {
		t.Fatalf("ACIndex failed: %v", err)
	}

	tests := []struct {
		text     string
		expected []string
	}{
		{"I like apple and banana", []string{"app", "apple", "banana"}},
		{"The appletree has an app", []string{"app", "apple", "tree", "appletree", "app"}},
		{"xylophone", []string{"x"}},
		{"cherry", nil},
		{"app", []string{"app"}},
	}

	for _, tt := range tests {
		refs := reader.MatchPostingRefs(tt.text)
		matches := make([]string, 0, len(refs))
		for _, ref := range refs {
			matches = append(matches, byRef[ref.Offset])
		}

		// Sort both slices to compare easily since AC output order might vary slightly
		if len(matches) > 0 {
			sort.Strings(matches)
		}
		if len(tt.expected) > 0 {
			sort.Strings(tt.expected)
		}

		if len(matches) == 0 && len(tt.expected) == 0 {
			continue
		}

		if !reflect.DeepEqual(matches, tt.expected) {
			t.Errorf("Text %q: expected %v, got %v", tt.text, tt.expected, matches)
		}
	}
}
