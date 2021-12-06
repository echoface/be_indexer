package bitsinvt

import (
	"github.com/RoaringBitmap/roaring/roaring64"
)

type (
	BEValue uint64

	RoaringPl struct {
		*roaring64.Bitmap
	}

	BEFieldContainer struct {
		wc  *RoaringPl
		inc map[BEValue]*RoaringPl
		exc map[BEValue]*RoaringPl
	}
)

func NewFieldContainer() *BEFieldContainer {
	return &BEFieldContainer{
		wc:  NewPostingList(),
		inc: make(map[BEValue]*RoaringPl),
		exc: make(map[BEValue]*RoaringPl),
	}
}

func NewPostingList() *RoaringPl {
	return &RoaringPl{
		Bitmap: roaring64.New(),
	}
}

func (c *BEFieldContainer) addWildcard(id ConjunctionID) {
	c.wc.Add(uint64(id))
}

func (c *BEFieldContainer) AddInclude(value BEValue, id ConjunctionID) {
	pl, ok := c.inc[value]
	if !ok {
		pl = NewPostingList()
		c.inc[value] = pl
	}
	pl.Add(uint64(id))
}

func (c *BEFieldContainer) AddExclude(value BEValue, id ConjunctionID) {
	pl, ok := c.exc[value]
	if !ok {
		pl = NewPostingList()
		c.exc[value] = pl
	}
	pl.Add(uint64(id))
	c.addWildcard(id)
}
