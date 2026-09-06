package manifest_test

import (
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/manifest"
)

func TestDocIDsSidecarRoundTrip(t *testing.T) {
	ids := []core.DocID{1, 9, 42}
	data := manifest.EncodeDocIDs(ids)
	got, err := manifest.DecodeDocIDs(data)
	if err != nil {
		t.Fatalf("DecodeDocIDs failed: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("ids length mismatch: got=%v want=%v", got, ids)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("ids mismatch: got=%v want=%v", got, ids)
		}
	}
}

func TestSidecarRejectsBadChecksum(t *testing.T) {
	data := manifest.EncodeDocIDs([]core.DocID{1})
	if err := manifest.VerifyBytes(data, uint64(len(data)), "sha256:bad"); err == nil {
		t.Fatal("expected checksum error")
	}
}
