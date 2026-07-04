package core_test

import (
	"testing"

	"github.com/echoface/be_indexer/core"
)

func TestConjIDEncodingBounds(t *testing.T) {
	if !core.ValidDocID(core.DocID(core.MaxDocID)) {
		t.Fatal("MaxDocID should be valid")
	}
	if core.ValidDocID(core.DocID(core.MaxDocID + 1)) {
		t.Fatal("MaxDocID+1 should be invalid")
	}
	if !core.ValidIdxOrSize(255) {
		t.Fatal("255 should be valid for 8-bit index/size")
	}
	if core.ValidIdxOrSize(256) {
		t.Fatal("256 should be invalid for 8-bit index/size")
	}

	cid := core.NewConjID(core.DocID(core.MaxDocID), 255, 255)
	if cid.DocID() != core.DocID(core.MaxDocID) || cid.Index() != 255 || cid.Size() != 255 {
		t.Fatalf("unexpected decoded conj id: %s", cid.String())
	}
}
