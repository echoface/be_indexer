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
func (c *DocIDCollector) Add(docID DocID, _ ConjID) {
	if c.liveDocs != nil && !c.liveDocs.IsAlive(docID) {
		return
	}
	c.docBits.Add(uint64(docID))
}

// DocCount returns the number of unique documents collected.
func (c *DocIDCollector) DocCount() int {
	return int(c.docBits.GetCardinality())
}

// GetDocIDs returns collected DocIDs as a sorted slice.
func (c *DocIDCollector) GetDocIDs() (ids DocIDList) {
	if c.DocCount() == 0 {
		return nil
	}
	ids = make(DocIDList, 0, c.DocCount())
	iter := c.docBits.Iterator()
	for iter.HasNext() {
		ids = append(ids, DocID(iter.Next()))
	}
	return ids
}

// GetDocIDsInto appends collected DocIDs into the given slice.
func (c *DocIDCollector) GetDocIDsInto(ids *DocIDList) {
	if c.DocCount() == 0 {
		return
	}
	iter := c.docBits.Iterator()
	for iter.HasNext() {
		*ids = append(*ids, DocID(iter.Next()))
	}
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
