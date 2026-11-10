package core

import (
	"sync"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// DocIDCollector collects unique DocIDs using a RoaringBitmap for dedup.
// It supports optional DocVisibilityMask filtering.
type DocIDCollector struct {
	docBits  *roaring64.Bitmap
	liveDocs *DocVisibilityMask
}

// NewDocIDCollector creates a DocIDCollector.
func NewDocIDCollector() *DocIDCollector {
	return &DocIDCollector{
		docBits: roaring64.New(),
	}
}

// AcceptsDuplicates lets the engine use this collector's bitmap for deduplication.
func (*DocIDCollector) AcceptsDuplicates() bool { return true }

// Reset clears the collector state.
func (c *DocIDCollector) Reset() {
	c.docBits.Clear()
	c.liveDocs = nil
}

// SetDocVisibilityMask attaches a visibility filter. Reset clears this setting;
// callers reusing the collector must attach the mask after each Reset.
func (c *DocIDCollector) SetDocVisibilityMask(ld *DocVisibilityMask) {
	c.liveDocs = ld
}

// Add implements ResultCollector. Skips hidden docs if DocVisibilityMask is set.
// DocIDCollector never saturates and always asks the engine to continue.
func (c *DocIDCollector) Add(docID DocID) CollectResult {
	if c.liveDocs != nil && !c.liveDocs.IsVisible(docID) {
		return CollectContinue
	}
	c.docBits.Add(uint64(docID))
	return CollectContinue
}

// DocCount returns the number of unique documents collected.
func (c *DocIDCollector) DocCount() int {
	return int(c.docBits.GetCardinality())
}

// Bitmap returns a borrowed view of the internal set. Reset and Add modify
// the same storage; call Clone to retain results across reuse or pool return.
func (c *DocIDCollector) Bitmap() *BitmapDocSet {
	return &BitmapDocSet{bits: c.docBits}
}

// collectorPool for reuse.
var collectorPool = sync.Pool{
	New: func() interface{} {
		return NewDocIDCollector()
	},
}

// PickCollector gets a collector from the pool.
func PickCollector() *DocIDCollector {
	return collectorPool.Get().(*DocIDCollector)
}

// PutCollector returns a collector to the pool.
func PutCollector(c *DocIDCollector) {
	if c == nil {
		return
	}
	c.Reset()
	collectorPool.Put(c)
}
