package roaringidx

import (
	"github.com/RoaringBitmap/roaring/roaring64"
	"sync"
)

type (
	BEValue uint64

	PostingList struct {
		*roaring64.Bitmap
	}
)

var bitmapPool = sync.Pool{
	New: func() interface{} {
		return roaring64.NewBitmap()
	},
}

func NewPostingList() PostingList {
	return PostingList{
		Bitmap: bitmapPool.Get().(*roaring64.Bitmap),
	}
}

func ReleasePostingList(list PostingList) {
	if list.Bitmap == nil {
		return
	}
	if !list.IsEmpty() {
		list.Clear()
	}
	bitmapPool.Put(list.Bitmap)
}
