package core

import (
	"github.com/RoaringBitmap/roaring/roaring64"
)

// BitmapDocSet is a compact roaring-backed DocID set used by the full+delta
// merge layer.
//
// It deliberately lives above BooleanEngine: a single BooleanEngine-level
// LiveDocs filter cannot represent update/recreate semantics across full and
// delta indexes, because it would filter the same DocID in both layers.
type BitmapDocSet struct {
	bits *roaring64.Bitmap
}

// NewBitmapDocSet creates a DocID set initialized with ids.
func NewBitmapDocSet(ids ...DocID) *BitmapDocSet {
	s := &BitmapDocSet{bits: roaring64.New()}
	for _, id := range ids {
		s.Add(id)
	}
	return s
}

// Add inserts a DocID into the set.
func (s *BitmapDocSet) Add(id DocID) {
	if s == nil {
		return
	}
	s.bits.Add(uint64(id))
}

// Remove deletes a DocID from the set.
func (s *BitmapDocSet) Remove(id DocID) {
	if s == nil {
		return
	}
	s.bits.Remove(uint64(id))
}

// Contains reports whether id is present.
func (s *BitmapDocSet) Contains(id DocID) bool {
	return s != nil && s.bits.Contains(uint64(id))
}

// Cardinality returns the number of DocIDs in the set.
func (s *BitmapDocSet) Cardinality() uint64 {
	if s == nil {
		return 0
	}
	return s.bits.GetCardinality()
}

// AndNot removes all elements in other from s (set difference: s = s \ other).
func (s *BitmapDocSet) AndNot(other *BitmapDocSet) {
	if s == nil || other == nil {
		return
	}
	s.bits.AndNot(other.bits)
}

// Or adds all elements in other to s (set union: s = s ∪ other).
func (s *BitmapDocSet) Or(other *BitmapDocSet) {
	if s == nil || other == nil {
		return
	}
	s.bits.Or(other.bits)
}

// Clone returns an independent copy that shares no state with s.
func (s *BitmapDocSet) Clone() *BitmapDocSet {
	if s == nil {
		return NewBitmapDocSet()
	}
	return &BitmapDocSet{bits: s.bits.Clone()}
}

// ForEach calls fn for every DocID in the set.
func (s *BitmapDocSet) ForEach(fn func(DocID)) {
	if s == nil || fn == nil {
		return
	}
	it := s.bits.Iterator()
	for it.HasNext() {
		fn(DocID(it.Next()))
	}
}
