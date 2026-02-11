package be_indexer

import (
	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer/core"
)

type (
	// DocIDCollector Default Collector with removing duplicated doc
	DocIDCollector struct {
		// docBits bitmap hold results docs
		docBits *roaring64.Bitmap
	}
)

func NewDocIDCollector() *DocIDCollector {
	return &DocIDCollector{
		docBits: roaring64.New(),
	}
}

func (c *DocIDCollector) DocCount() int {
	return int(c.docBits.GetCardinality())
}

func (c *DocIDCollector) Reset() {
	c.docBits.Clear()
}

func (c *DocIDCollector) Add(docID core.DocID, _ core.ConjID) {
	c.docBits.Add(uint64(docID))
}

func (c *DocIDCollector) GetDocIDs() (ids core.DocIDList) {
	if c.DocCount() == 0 {
		return nil
	}
	ids = make(core.DocIDList, 0, c.DocCount())
	iter := c.docBits.Iterator()
	for iter.HasNext() {
		ids = append(ids, core.DocID(iter.Next()))
	}
	return ids
}

func (c *DocIDCollector) GetDocIDsInto(ids *core.DocIDList) {
	if c.DocCount() == 0 {
		return
	}
	iter := c.docBits.Iterator()
	for iter.HasNext() {
		*ids = append(*ids, core.DocID(iter.Next()))
	}
	return
}

