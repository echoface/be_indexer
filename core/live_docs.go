package core

import (
	"github.com/RoaringBitmap/roaring/roaring64"
)

// LiveDocs tracks which documents are alive (not deleted).
// Uses a RoaringBitmap to store deleted DocIDs — a document is alive
// if its ID is NOT present in the bitmap.
type LiveDocs struct {
	deleted *roaring64.Bitmap
}

// NewLiveDocs creates a LiveDocs with no deletions.
func NewLiveDocs() *LiveDocs {
	return &LiveDocs{
		deleted: roaring64.New(),
	}
}

// IsAlive returns true if the given DocID has not been marked as deleted.
func (ld *LiveDocs) IsAlive(docID DocID) bool {
	return !ld.deleted.Contains(uint64(docID))
}

// MarkDeleted marks a DocID as deleted.
func (ld *LiveDocs) MarkDeleted(docID DocID) {
	ld.deleted.Add(uint64(docID))
}

// DeletedCount returns the number of deleted documents.
func (ld *LiveDocs) DeletedCount() uint64 {
	return ld.deleted.GetCardinality()
}

// HasDeletions returns true if any documents have been deleted.
func (ld *LiveDocs) HasDeletions() bool {
	return !ld.deleted.IsEmpty()
}

// Serialize serializes the deletion bitmap to bytes.
func (ld *LiveDocs) Serialize() ([]byte, error) {
	return ld.deleted.MarshalBinary()
}

// Deserialize restores the deletion bitmap from bytes.
func (ld *LiveDocs) Deserialize(data []byte) error {
	bm := roaring64.New()
	if err := bm.UnmarshalBinary(data); err != nil {
		return err
	}
	ld.deleted = bm
	return nil
}

// Clone returns a deep copy of the LiveDocs.
func (ld *LiveDocs) Clone() *LiveDocs {
	return &LiveDocs{
		deleted: ld.deleted.Clone(),
	}
}
