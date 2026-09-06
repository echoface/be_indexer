package core

import (
	"sync"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// DocIDCollector collects unique DocIDs using a RoaringBitmap for dedup.
// It supports optional LiveDocs filtering.
type DocIDCollector struct {
	docBits  *roaring64.Bitmap
	liveDocs *LiveDocs
}

// NewDocIDCollector creates a DocIDCollector.
func NewDocIDCollector() *DocIDCollector {
	return &DocIDCollector{
		docBits: roaring64.New(),
	}
}

// Reset clears the collector state.
func (c *DocIDCollector) Reset() {
	c.docBits.Clear()
	c.liveDocs = nil
}

// SetLiveDocs attaches a LiveDocs filter to this collector.
func (c *DocIDCollector) SetLiveDocs(ld *LiveDocs) {
	c.liveDocs = ld
}

// Add implements ResultCollector. Skips deleted docs if LiveDocs is set.
func (c *DocIDCollector) Add(docID DocID) {
	if c.liveDocs != nil && !c.liveDocs.IsAlive(docID) {
		return
	}
	c.docBits.Add(uint64(docID))
}

// DocCount returns the number of unique documents collected.
func (c *DocIDCollector) DocCount() int {
	return int(c.docBits.GetCardinality())
}

// Bitmap returns the internal BitmapDocSet. The returned set shares the
// underlying bitmap with the collector; callers must not mutate it after
// the collector is returned to the pool.
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
