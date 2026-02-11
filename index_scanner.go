package be_indexer

import (
	"encoding/binary"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/util"
)

/*
IndexScan
a scanner for indexer, it helps to retrieve result document id from posting entries
currently, it is used by core.BEIndex, but as a part of design, it should top on core.BEIndex
that seems more reasonable. so may be next version, should be refactored(fixed).
*/

const (
	LinearSkipDistance = 8
)

type (
	EntriesCursors []core.PostingIterator

	// FieldCursor for a boolean expression: {"tag", "in", [1, 2, 3]}
	// tag_2: [ID5]
	// tag_1: [ID1, ID2, ID7]
	FieldCursor struct {
		current     core.PostingIterator
		cursorGroup EntriesCursors
	}
	FieldCursors []FieldCursor
)

var (
	_ core.TermIterator    = (*SliceIterator)(nil)
	_ core.TermIterator    = (*BlockIterator)(nil)
	_ core.TermIterator    = (*RoaringIterator)(nil)
	_ core.FieldIterator   = (*FieldCursor)(nil)
	_ core.PostingIterator = (*SliceIterator)(nil)
	_ core.PostingIterator = (*BlockIterator)(nil)
	_ core.PostingIterator = (*RoaringIterator)(nil)
)

func NewTerm(field core.BEField, v interface{}) core.Term {
	key := core.Term{Field: field, Value: v}
	return key
}

// --------------------------------------------------------------------------------
// SliceIterator: 用于未压缩的内存数组
// --------------------------------------------------------------------------------

type SliceIterator struct {
	term    core.Term
	entries core.Entries
	cursor  int
	curEID  core.EntryID
	idSize  int
}

func NewSliceIterator(term core.Term, entries core.Entries) *SliceIterator {
	it := &SliceIterator{
		term:    term,
		entries: entries,
		cursor:  0,
		idSize:  len(entries),
		curEID:  core.NULLENTRY,
	}
	if len(entries) > 0 {
		it.curEID = entries[0]
	}
	return it
}

func (it *SliceIterator) Term() core.Term { return it.term }

func (it *SliceIterator) Current() core.EntryID { return it.curEID }

func (it *SliceIterator) SkipTo(target core.EntryID) core.EntryID {
	if it.curEID >= target {
		return it.curEID
	}
	if it.cursor >= it.idSize {
		return core.NULLENTRY
	}

	// 优化：利用 galloping search
	oc := it.cursor
	bound := 1
	rightSideIndex := oc + bound

	for rightSideIndex < it.idSize && it.entries[rightSideIndex] < target {
		it.cursor = rightSideIndex
		bound = bound << 1
		rightSideIndex = oc + bound
	}
	if rightSideIndex > it.idSize {
		rightSideIndex = it.idSize
	}

	// 二分查找区间 [it.cursor, rightSideIndex)
	for it.cursor < rightSideIndex && it.entries[it.cursor] < target {
		bound = (it.cursor + rightSideIndex) >> 1
		if it.entries[bound] >= target {
			rightSideIndex = bound
		} else {
			it.cursor = bound + 1
		}
	}

	if it.cursor >= it.idSize {
		it.curEID = core.NULLENTRY
	} else {
		it.curEID = it.entries[it.cursor]
	}
	return it.curEID
}

// --------------------------------------------------------------------------------
// BlockIterator: 用于压缩数据
// --------------------------------------------------------------------------------

type BlockIterator struct {
	term           core.Term
	headers        []util.CompHeader
	compressedData []byte

	currentHeader int
	headerEnd     int

	decompBuffer [128]core.EntryID
	bufCursor    int
	bufLen       int

	curEID core.EntryID
}

func NewBlockIterator(term core.Term, headers []util.CompHeader, compressedData []byte, headerEnd int) *BlockIterator {
	it := &BlockIterator{
		term:           term,
		headers:        headers,
		compressedData: compressedData,
		headerEnd:      headerEnd,
		currentHeader:  -1,
		curEID:         core.NULLENTRY,
	}
	return it
}

func (it *BlockIterator) LoadBlock(headerIdx int) {
	it.loadBlock(headerIdx)
}

func (it *BlockIterator) loadBlock(headerIdx int) {
	header := it.headers[headerIdx]
	offset := header.Offset
	data := it.compressedData[offset:]

	// 1. First ID
	val, n := binary.Uvarint(data)
	data = data[n:]
	base := val

	it.decompBuffer[0] = core.EntryID(base)
	it.bufLen = 1

	// 2. Deltas
	for {
		if core.EntryID(base) == core.EntryID(header.LastEntryID) {
			break
		}
		if it.bufLen >= len(it.decompBuffer) {
			break
		}
		delta, n := binary.Uvarint(data)
		data = data[n:]
		base += delta
		it.decompBuffer[it.bufLen] = core.EntryID(base)
		it.bufLen++
	}

	it.currentHeader = headerIdx
	it.bufCursor = 0
	it.curEID = it.decompBuffer[0]
}

func (it *BlockIterator) Term() core.Term { return it.term }

func (it *BlockIterator) Current() core.EntryID { return it.curEID }

func (it *BlockIterator) SkipTo(target core.EntryID) core.EntryID {
	if it.curEID >= target {
		return it.curEID
	}

	// 1. 检查当前 Buffer
	if it.bufCursor < it.bufLen {
		if it.decompBuffer[it.bufLen-1] >= target {
			return it.searchInBuffer(target)
		}
	}

	// 2. 检查后续 Headers
	startIdx := it.currentHeader + 1
	if it.currentHeader == -1 {
		startIdx = 0
	}

	for i := startIdx; i < it.headerEnd; i++ {
		if core.EntryID(it.headers[i].LastEntryID) >= target {
			it.loadBlock(i)
			return it.searchInBuffer(target)
		}
	}

	it.curEID = core.NULLENTRY
	return core.NULLENTRY
}

func (it *BlockIterator) searchInBuffer(target core.EntryID) core.EntryID {
	if it.decompBuffer[it.bufLen-1] < target {
		it.bufCursor = it.bufLen
		it.curEID = core.NULLENTRY
		return core.NULLENTRY
	}

	// 线性扫描（因为 buffer 很小，通常比二分快或差不多）
	for it.bufCursor < it.bufLen {
		if it.decompBuffer[it.bufCursor] >= target {
			it.curEID = it.decompBuffer[it.bufCursor]
			return it.curEID
		}
		it.bufCursor++
	}

	it.curEID = core.NULLENTRY
	return core.NULLENTRY
}

// --------------------------------------------------------------------------------
// RoaringIterator: 用于 RoaringBitmap
// --------------------------------------------------------------------------------

type RoaringIterator struct {
	term   core.Term
	iter   roaring64.IntIterable64
	curEID core.EntryID
}

func NewRoaringIterator(term core.Term, bitmap *roaring64.Bitmap) *RoaringIterator {
	it := &RoaringIterator{
		term: term,
		iter: bitmap.Iterator(),
	}
	if it.iter.HasNext() {
		it.curEID = core.EntryID(it.iter.Next())
	} else {
		it.curEID = core.NULLENTRY
	}
	return it
}

func (it *RoaringIterator) Term() core.Term { return it.term }

func (it *RoaringIterator) Current() core.EntryID { return it.curEID }

func (it *RoaringIterator) SkipTo(target core.EntryID) core.EntryID {
	if it.curEID >= target {
		return it.curEID
	}

	// RoaringBitmap Iterator 仅支持线性 Next
	// 对于非常稠密的数据，可能需要优化（目前库不支持高效 Skip）
	for it.curEID < target && !it.curEID.IsNULLEntry() {
		if it.iter.HasNext() {
			it.curEID = core.EntryID(it.iter.Next())
		} else {
			it.curEID = core.NULLENTRY
		}
	}
	return it.curEID
}

// --------------------------------------------------------------------------------
// 兼容层：EntriesCursor (仅保留以维持对部分存量代码的签名兼容，建议迁移)
// --------------------------------------------------------------------------------

// FieldCursor 逻辑保持不变
// --------------------------------------------------------------------------------

// Len FieldCursors sort API
func (s FieldCursors) Len() int      { return len(s) }
func (s FieldCursors) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FieldCursors) Less(i, j int) bool {
	return s[i].Current() < s[j].Current()
}

func (s FieldCursors) Sort() {
	x := len(s)
	if x <= 1 {
		return
	}
	for i := 1; i < x; i++ {
		for j := i; j > 0 && s[j].Current() < s[j-1].Current(); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func NewFieldCursor(cursors ...core.PostingIterator) FieldCursor {
	scanner := FieldCursor{
		current:     nil,
		cursorGroup: cursors,
	}
	for _, cursor := range scanner.cursorGroup {
		if scanner.current == nil || cursor.Current() < scanner.current.Current() {
			scanner.current = cursor
		}
	}
	return scanner
}

func (fc *FieldCursor) ReachEnd() bool {
	return fc.current == nil || fc.current.Current().IsNULLEntry()
}

func (fc *FieldCursor) Current() core.EntryID {
	if fc.current == nil {
		return core.NULLENTRY
	}
	return fc.current.Current()
}

func (fc *FieldCursor) GetCurEntryID() core.EntryID {
	return fc.Current()
}

func (fc *FieldCursor) SkipTo(id core.EntryID) (newMin core.EntryID) {
	newMin = core.NULLENTRY
	for _, cur := range fc.cursorGroup {
		if eid := cur.SkipTo(id); eid <= newMin {
			newMin = eid
			fc.current = cur
		}
	}
	return
}
