package core

import (
	"github.com/RoaringBitmap/roaring/roaring64"
)

// DocVisibilityMask stores DocIDs hidden from one engine or collector.
// A hidden ID can represent a deletion or a version superseded in a newer delta.
// IDs absent from the bitmap are visible; this does not test index membership.
// Configure the mask before querying and do not mutate it concurrently with reads.
type DocVisibilityMask struct {
	deleted *roaring64.Bitmap
}

// NewDocVisibilityMask creates a mask in which every DocID is visible.
func NewDocVisibilityMask() *DocVisibilityMask {
	return &DocVisibilityMask{
		deleted: roaring64.New(),
	}
}

// IsVisible reports whether docID is absent from this mask's hidden-ID set.
func (ld *DocVisibilityMask) IsVisible(docID DocID) bool {
	return !ld.deleted.Contains(uint64(docID))
}

// Hide excludes docID from results filtered by this mask; it does not modify
// stored documents or remove the ID from any other index layer.
func (ld *DocVisibilityMask) Hide(docID DocID) {
	ld.deleted.Add(uint64(docID))
}

// DeletedCount returns the number of hidden DocIDs, including superseded versions.
func (ld *DocVisibilityMask) DeletedCount() uint64 {
	return ld.deleted.GetCardinality()
}

// HasDeletions reports whether the mask contains any hidden DocIDs.
func (ld *DocVisibilityMask) HasDeletions() bool {
	return !ld.deleted.IsEmpty()
}

// Serialize serializes the hidden-ID bitmap to bytes.
func (ld *DocVisibilityMask) Serialize() ([]byte, error) {
	return ld.deleted.MarshalBinary()
}

// Deserialize restores the hidden-ID bitmap from bytes.
func (ld *DocVisibilityMask) Deserialize(data []byte) error {
	bm := roaring64.New()
	if err := bm.UnmarshalBinary(data); err != nil {
		return err
	}
	ld.deleted = bm
	return nil
}

// Clone returns a deep copy of the DocVisibilityMask.
func (ld *DocVisibilityMask) Clone() *DocVisibilityMask {
	return &DocVisibilityMask{
		deleted: ld.deleted.Clone(),
	}
}
