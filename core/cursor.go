package core

import "sort"

// PostingIterator is defined in core already
// Let's implement SliceIterator and FieldCursor here.

// SliceIterator a wrap for slice EntryID
type SliceIterator struct {
	cursor int
	term   Term
	EIDs   []EntryID
}

func NewSliceIterator(term Term, eids []EntryID) *SliceIterator {
	return &SliceIterator{
		cursor: 0,
		term:   term,
		EIDs:   eids,
	}
}

func (s *SliceIterator) Term() Term {
	return s.term
}

func (s *SliceIterator) Current() EntryID {
	if s.cursor >= len(s.EIDs) {
		return NULLENTRY
	}
	return s.EIDs[s.cursor]
}

func (s *SliceIterator) SkipTo(id EntryID) EntryID {
	// Binary search for efficiency
	left, right := s.cursor, len(s.EIDs)-1
	for left <= right {
		mid := left + (right-left)/2
		if s.EIDs[mid] < id {
			left = mid + 1
		} else {
			right = mid - 1
		}
	}
	s.cursor = left
	return s.Current()
}

type FieldCursor struct {
	Iters []PostingIterator
}

func NewFieldCursor(iters ...PostingIterator) FieldCursor {
	fc := FieldCursor{Iters: make([]PostingIterator, 0, len(iters))}
	for _, it := range iters {
		if it != nil && !it.Current().IsNULLEntry() {
			fc.Iters = append(fc.Iters, it)
		}
	}
	fc.Sort()
	return fc
}

func (f *FieldCursor) Sort() {
	sort.Slice(f.Iters, func(i, j int) bool {
		return f.Iters[i].Current() < f.Iters[j].Current()
	})
}

func (f *FieldCursor) GetCurEntryID() EntryID {
	if len(f.Iters) == 0 {
		return NULLENTRY
	}
	return f.Iters[0].Current()
}

func (f *FieldCursor) SkipTo(id EntryID) EntryID {
	if len(f.Iters) == 0 {
		return NULLENTRY
	}
	res := f.Iters[0].SkipTo(id)
	f.Sort()
	return res
}

func (f *FieldCursor) DumpInfo() []string {
	var res []string
	for _, it := range f.Iters {
		if it == nil || it.Current().IsNULLEntry() {
			continue
		}
		res = append(res, it.Current().DocString())
	}
	return res
}

type FieldCursors []FieldCursor

func (f FieldCursors) Sort() {
	sort.Slice(f, func(i, j int) bool {
		return f[i].GetCurEntryID() < f[j].GetCurEntryID()
	})
}
