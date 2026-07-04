package segment

import (
	"testing"
)

func TestFlatDict(t *testing.T) {
	m := map[string]PostingRef{
		"apple":  {Offset: 100, Count: 1},
		"banana": {Offset: 200, Count: 2},
		"cherry": {Offset: 300, Count: 3},
		"date":   {Offset: 400, Count: 4},
	}

	buf := WriteFlatDict(m)

	dict, err := NewFlatDict(buf)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		key   string
		found bool
		ref   PostingRef
	}{
		{"apple", true, PostingRef{Offset: 100, Count: 1}},
		{"banana", true, PostingRef{Offset: 200, Count: 2}},
		{"cherry", true, PostingRef{Offset: 300, Count: 3}},
		{"date", true, PostingRef{Offset: 400, Count: 4}},
		{"cat", false, PostingRef{}},
		{"zebra", false, PostingRef{}},
	}

	for _, tt := range tests {
		ref, ok := dict.Find([]byte(tt.key))
		if ok != tt.found {
			t.Errorf("key %s found expected %v, got %v", tt.key, tt.found, ok)
		}
		if ok && ref != tt.ref {
			t.Errorf("key %s ref expected %+v, got %+v", tt.key, tt.ref, ref)
		}
	}
}
