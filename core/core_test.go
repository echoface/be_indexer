package core

import (
	"bytes"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// ConjID encoding / decoding
// ---------------------------------------------------------------------------

func TestConjID_EncodeDecode(t *testing.T) {
	cases := []struct {
		docID DocID
		index int
		size  int
	}{
		{0, 0, 0},
		{1, 2, 3},
		{2, 0, 1},
		{12, 1, 20},
		{-111, 1, 20},
		{MaxDocID, 255, 255},
		{-MaxDocID, 255, 255},
		{1000000, 128, 64},
		{-1, 0, 0},
	}
	for _, cs := range cases {
		id := NewConjID(cs.docID, cs.index, cs.size)
		if got := id.DocID(); got != cs.docID {
			t.Errorf("DocID mismatch: input=%d got=%d", cs.docID, got)
		}
		if got := id.Index(); got != cs.index {
			t.Errorf("Index mismatch: input=%d got=%d", cs.index, got)
		}
		if got := id.Size(); got != cs.size {
			t.Errorf("Size mismatch: input=%d got=%d", cs.size, got)
		}
	}
}

func TestConjID_String(t *testing.T) {
	id := NewConjID(12, 1, 20)
	want := "<12,1,20>"
	if got := id.String(); got != want {
		t.Errorf("String mismatch: got=%s want=%s", got, want)
	}
}

func TestConjID_EdgeCases_PanicOnOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for DocID > MaxDocID")
		}
	}()
	NewConjID(MaxDocID+1, 0, 0)
}

func TestConjID_EdgeCases_PanicOnIndexOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for index >= 256")
		}
	}()
	NewConjID(1, 256, 0)
}

func TestConjID_EdgeCases_PanicOnSizeOverflow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for size >= 256")
		}
	}()
	NewConjID(1, 0, 256)
}

func TestConjID_NegativeDocID(t *testing.T) {
	// Negative DocID should preserve the full bit pattern.
	id := NewConjID(-12345, 5, 10)
	if id.DocID() != -12345 {
		t.Errorf("Negative DocID mismatch: got=%d want=-12345", id.DocID())
	}
	if id.Index() != 5 {
		t.Errorf("Index mismatch for negative DocID: got=%d want=5", id.Index())
	}
	if id.Size() != 10 {
		t.Errorf("Size mismatch for negative DocID: got=%d want=10", id.Size())
	}
}

func TestValidDocID(t *testing.T) {
	if !ValidDocID(0) {
		t.Error("0 should be valid")
	}
	if !ValidDocID(MaxDocID) {
		t.Error("MaxDocID should be valid")
	}
	if !ValidDocID(-MaxDocID) {
		t.Error("-MaxDocID should be valid")
	}
	if ValidDocID(MaxDocID + 1) {
		t.Error("MaxDocID+1 should be invalid")
	}
	if ValidDocID(-MaxDocID - 1) {
		t.Error("-MaxDocID-1 should be invalid")
	}
}

func TestValidIdxOrSize(t *testing.T) {
	if !ValidIdxOrSize(0) {
		t.Error("0 should be valid")
	}
	if !ValidIdxOrSize(255) {
		t.Error("255 should be valid")
	}
	if ValidIdxOrSize(256) {
		t.Error("256 should be invalid")
	}
	if ValidIdxOrSize(-1) {
		t.Error("-1 should be invalid")
	}
}

// ---------------------------------------------------------------------------
// EntryID encoding / decoding
// ---------------------------------------------------------------------------

func TestEntryID_IncludeExclude(t *testing.T) {
	conjID := NewConjID(100, 3, 7)

	incl := NewEntryID(conjID, true)
	if !incl.IsInclude() {
		t.Errorf("Include entry should report IsInclude=true")
	}
	if incl.IsExclude() {
		t.Errorf("Include entry should report IsExclude=false")
	}
	if got := incl.GetConjID(); got != conjID {
		t.Errorf("ConjID extraction failed: got=%v want=%v", got, conjID)
	}

	excl := NewEntryID(conjID, false)
	if excl.IsInclude() {
		t.Errorf("Exclude entry should report IsInclude=false")
	}
	if !excl.IsExclude() {
		t.Errorf("Exclude entry should report IsExclude=true")
	}
	if got := excl.GetConjID(); got != conjID {
		t.Errorf("ConjID extraction failed: got=%v want=%v", got, conjID)
	}
}

func TestEntryID_Ordering(t *testing.T) {
	// Include entries should sort AFTER exclude entries for the same ConjID
	conjID := NewConjID(5, 1, 2)
	incl := NewEntryID(conjID, true)
	excl := NewEntryID(conjID, false)
	if incl <= excl {
		t.Errorf("Include entry should be > Exclude entry for same ConjID: incl=%d excl=%d", incl, excl)
	}
}

func TestEntryID_NULLENTRY(t *testing.T) {
	if !NULLENTRY.IsNULLEntry() {
		t.Error("NULLENTRY should report IsNULLEntry=true")
	}
	if NewEntryID(NewConjID(1, 0, 0), true).IsNULLEntry() {
		t.Error("normal entry should not be NULLENTRY")
	}
}

func TestEntryID_DocString(t *testing.T) {
	conjID := NewConjID(10, 2, 3)
	incl := NewEntryID(conjID, true)
	s := incl.DocString()
	if len(s) == 0 {
		t.Error("DocString should not be empty")
	}
}

func TestEntries_Sort(t *testing.T) {
	conjID1 := NewConjID(1, 0, 0)
	conjID2 := NewConjID(2, 0, 0)
	entries := Entries{
		NewEntryID(conjID2, true),
		NewEntryID(conjID1, true),
		NewEntryID(conjID1, false),
	}
	sort.Sort(entries)
	if !sort.IsSorted(entries) {
		t.Error("Entries should be sorted")
	}
	// First entry should be the exclude entry for conjID1
	if entries[0].IsInclude() {
		t.Error("First sorted entry should be Exclude")
	}
	if entries[0].GetConjID() != conjID1 {
		t.Error("First sorted entry should have conjID1")
	}
}

func TestEntries_DocString(t *testing.T) {
	conjID := NewConjID(1, 0, 0)
	entries := Entries{
		NewEntryID(conjID, true),
		NewEntryID(conjID, false),
	}
	strs := entries.DocString()
	if len(strs) != 2 {
		t.Errorf("DocString should return 2 strings, got %d", len(strs))
	}
}

// ---------------------------------------------------------------------------
// LiveDocs
// ---------------------------------------------------------------------------

func TestLiveDocs_Basic(t *testing.T) {
	ld := NewLiveDocs()

	if !ld.IsAlive(1) {
		t.Error("DocID 1 should be alive initially")
	}
	if ld.HasDeletions() {
		t.Error("No deletions should be reported initially")
	}
	if ld.DeletedCount() != 0 {
		t.Errorf("DeletedCount should be 0 initially, got %d", ld.DeletedCount())
	}

	ld.MarkDeleted(1)
	if ld.IsAlive(1) {
		t.Error("DocID 1 should be dead after MarkDeleted")
	}
	if ld.IsAlive(2) {
		// Doc 2 should still be alive
	}
	if !ld.HasDeletions() {
		t.Error("HasDeletions should report true after deletion")
	}
	if ld.DeletedCount() != 1 {
		t.Errorf("DeletedCount should be 1, got %d", ld.DeletedCount())
	}
}

func TestLiveDocs_MultipleDeletions(t *testing.T) {
	ld := NewLiveDocs()
	deleted := []DocID{10, 20, 30, 100, 200}
	for _, id := range deleted {
		ld.MarkDeleted(id)
	}
	if int(ld.DeletedCount()) != len(deleted) {
		t.Errorf("DeletedCount=%d, want %d", ld.DeletedCount(), len(deleted))
	}
	for _, id := range deleted {
		if ld.IsAlive(id) {
			t.Errorf("DocID %d should be dead", id)
		}
	}
	// DocID 0 was not deleted
	if !ld.IsAlive(0) {
		t.Error("DocID 0 should be alive")
	}
}

func TestLiveDocs_SerializeDeserialize(t *testing.T) {
	ld := NewLiveDocs()
	ld.MarkDeleted(5)
	ld.MarkDeleted(10)
	ld.MarkDeleted(100)

	data, err := ld.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	ld2 := NewLiveDocs()
	if err := ld2.Deserialize(data); err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	if ld2.DeletedCount() != 3 {
		t.Errorf("DeletedCount mismatch after deserialize: %d", ld2.DeletedCount())
	}
	if !ld2.IsAlive(1) {
		t.Error("DocID 1 should still be alive")
	}
	if ld2.IsAlive(5) {
		t.Error("DocID 5 should be dead after deserialize")
	}
	if ld2.IsAlive(100) {
		t.Error("DocID 100 should be dead after deserialize")
	}
}
func TestLiveDocs_Clone(t *testing.T) {
	ld := NewLiveDocs()
	ld.MarkDeleted(42)

	clone := ld.Clone()
	if !clone.IsAlive(1) {
		t.Error("Clone: DocID 1 should be alive")
	}
	if clone.IsAlive(42) {
		t.Error("Clone: DocID 42 should be dead")
	}

	// Verify clone has independent state
	clone.MarkDeleted(99)
	if !ld.IsAlive(99) {
		t.Error("Clone mutation should not affect original")
	}
	// ---------------------------------------------------------------------------
}

// DocIDCollector
// ---------------------------------------------------------------------------

func TestDocIDCollector_Basic(t *testing.T) {
	c := NewDocIDCollector()
	if c.DocCount() != 0 {
		t.Errorf("Initial count should be 0, got %d", c.DocCount())
	}

	c.Add(1)
	c.Add(2)
	c.Add(1) // duplicate
	if c.DocCount() != 2 {
		t.Errorf("Count should be 2 (dedup), got %d", c.DocCount())
	}

	ids := bitmapToSlice(c.Bitmap())
	if len(ids) != 2 {
		t.Fatalf("GetDocIDs should return 2 ids, got %d", len(ids))
	}
	if ids[0] != 1 || ids[1] != 2 {
		t.Errorf("Unexpected doc ids: %v", ids)
	}
}

func TestDocIDCollector_Reset(t *testing.T) {
	c := NewDocIDCollector()
	c.Add(1)
	c.Reset()
	if c.DocCount() != 0 {
		t.Errorf("After reset, count should be 0, got %d", c.DocCount())
	}
}

func TestDocIDCollector_GetDocIDsInto(t *testing.T) {
	c := NewDocIDCollector()
	c.Add(10)
	c.Add(20)

	var into DocIDList
	into = append(into, bitmapToSlice(c.Bitmap())...)
	if len(into) != 2 {
		t.Fatalf("GetDocIDsInto should append 2 ids, got %d", len(into))
	}
	if into[0] != 10 || into[1] != 20 {
		t.Errorf("Unexpected ids: %v", into)
	}
}

func TestDocIDCollector_Empty(t *testing.T) {
	c := NewDocIDCollector()
	if ids := bitmapToSlice(c.Bitmap()); ids != nil {
		t.Errorf("Empty collector should return nil, got %v", ids)
	}
	var into DocIDList
	into = append(into, bitmapToSlice(c.Bitmap())...)
	if len(into) != 0 {
		t.Errorf("Empty collector GetDocIDsInto should not append")
	}
}

func TestDocIDCollector_WithLiveDocs(t *testing.T) {
	ld := NewLiveDocs()
	ld.MarkDeleted(2)
	ld.MarkDeleted(3)

	c := NewDocIDCollector()
	c.SetLiveDocs(ld)

	c.Add(1) // alive
	c.Add(2) // dead → filtered
	c.Add(3) // dead → filtered
	c.Add(4) // alive

	if c.DocCount() != 2 {
		t.Errorf("Count should be 2 (filtered), got %d", c.DocCount())
	}

	ids := bitmapToSlice(c.Bitmap())
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 4 {
		t.Errorf("Expected [1 4], got %v", ids)
	}
}

func TestDocIDCollector_Pool(t *testing.T) {
	c := PickCollector()
	c.Add(42)
	PutCollector(c)

	c2 := PickCollector()
	if c2.DocCount() != 0 {
		t.Errorf("Pooled collector should be reset, count=%d", c2.DocCount())
	}
	PutCollector(c2)
}

// ---------------------------------------------------------------------------
// SliceIterator
// ---------------------------------------------------------------------------

func TestSliceIterator_Basic(t *testing.T) {
	term := NewTerm("age", 25)
	entries := []EntryID{
		NewEntryID(NewConjID(1, 0, 0), true),
		NewEntryID(NewConjID(2, 0, 0), true),
		NewEntryID(NewConjID(3, 0, 0), true),
	}
	it := NewSliceIterator(term, entries)

	if it.Term() != term {
		t.Errorf("Term mismatch")
	}
	if it.Current() != entries[0] {
		t.Errorf("Current should be first entry")
	}
	if it.Current() != entries[0] {
		t.Errorf("Current should not advance without SkipTo")
	}
}

func TestSliceIterator_SkipTo(t *testing.T) {
	entries := []EntryID{
		NewEntryID(NewConjID(1, 0, 1), true),
		NewEntryID(NewConjID(2, 0, 1), true),
		NewEntryID(NewConjID(3, 0, 1), true),
		NewEntryID(NewConjID(5, 0, 1), true),
		NewEntryID(NewConjID(8, 0, 1), true),
		NewEntryID(NewConjID(13, 0, 1), true),
	}
	it := NewSliceIterator(NewTerm("x", 0), entries)

	// SkipTo entry[3] (conjID=5)
	target := entries[3]
	got := it.SkipTo(target)
	if got != target {
		t.Errorf("SkipTo(%d) returned %d, want %d", target, got, target)
	}

	// SkipTo a value between entries[4] and entries[5]
	between := NewEntryID(NewConjID(10, 0, 1), true)
	got = it.SkipTo(between)
	if got != entries[5] {
		t.Errorf("SkipTo(%d) returned %d, want %d", between, got, entries[5])
	}

	// SkipTo past end
	pastEnd := NewEntryID(NewConjID(100, 0, 1), true)
	got = it.SkipTo(pastEnd)
	if !got.IsNULLEntry() {
		t.Errorf("SkipTo past end should return NULLENTRY, got %d", got)
	}
}

func TestSliceIterator_Empty(t *testing.T) {
	it := NewSliceIterator(NewTerm("x", 0), nil)
	if !it.Current().IsNULLEntry() {
		t.Error("Empty iterator should return NULLENTRY from Current()")
	}
}

func TestSliceIterator_SkipToAlreadyAt(t *testing.T) {
	entries := []EntryID{
		NewEntryID(NewConjID(5, 0, 0), true),
		NewEntryID(NewConjID(10, 0, 0), true),
	}
	it := NewSliceIterator(NewTerm("x", 0), entries)

	// SkipTo current position should stay
	it.SkipTo(entries[0])
	if got := it.Current(); got != entries[0] {
		t.Errorf("Should stay at first entry: got=%d", got)
	}

	// SkipTo exactly the next entry
	it.SkipTo(entries[1])
	if got := it.Current(); got != entries[1] {
		t.Errorf("Should advance to second entry: got=%d", got)
	}
}

// ---------------------------------------------------------------------------
// FieldCursor
// ---------------------------------------------------------------------------

func TestFieldCursor_Basic(t *testing.T) {
	entries1 := []EntryID{NewEntryID(NewConjID(10, 0, 1), true)}
	entries2 := []EntryID{NewEntryID(NewConjID(5, 0, 1), false)}

	it1 := NewSliceIterator(NewTerm("a", 0), entries1)
	it2 := NewSliceIterator(NewTerm("b", 0), entries2)

	fc := NewFieldCursor(it1, it2)
	// After sort, the smaller EntryID should be first (exclude entry = smaller)
	first := fc.GetCurEntryID()
	if first != entries2[0] {
		t.Errorf("Smallest cursor should be first: got=%d want=%d", first, entries2[0])
	}
}

func TestFieldCursor_SkipTo(t *testing.T) {
	e1 := NewEntryID(NewConjID(3, 0, 1), true)
	e2 := NewEntryID(NewConjID(5, 0, 1), true)
	e3 := NewEntryID(NewConjID(7, 0, 1), true)

	it := NewSliceIterator(NewTerm("x", 0), []EntryID{e1, e2, e3})
	fc := NewFieldCursor(it)

	// Skip to middle
	got := fc.SkipTo(e2)
	if got != e2 {
		t.Errorf("SkipTo(%d) returned %d, want %d", e2, got, e2)
	}

	// Skip past end
	got = fc.SkipTo(NewEntryID(NewConjID(100, 0, 1), true))
	if !got.IsNULLEntry() {
		t.Errorf("SkipTo past end should return NULLENTRY, got %d", got)
	}
}

func TestFieldCursor_MultipleIterators(t *testing.T) {
	// Two iterators with overlapping EntryIDs
	// The FieldCursor's Current() should always return the smallest EntryID
	a := NewSliceIterator(NewTerm("a", 0), []EntryID{
		NewEntryID(NewConjID(1, 0, 1), true),
		NewEntryID(NewConjID(5, 0, 1), true),
	})
	b := NewSliceIterator(NewTerm("b", 0), []EntryID{
		NewEntryID(NewConjID(3, 0, 1), true),
		NewEntryID(NewConjID(7, 0, 1), true),
	})

	fc := NewFieldCursor(a, b)
	// After sort: a points to 1 (smallest)
	if got := fc.GetCurEntryID(); got != NewEntryID(NewConjID(1, 0, 1), true) {
		t.Errorf("Expected entry for conjID=1")
	}

	// Skip past 1
	fc.SkipTo(NewEntryID(NewConjID(2, 0, 1), true))
	// Now a points to 5, b points to 3 → smallest is 3
	if got := fc.GetCurEntryID(); got != NewEntryID(NewConjID(3, 0, 1), true) {
		t.Errorf("Expected entry for conjID=3")
	}
	// After SkipTo(cj2): fc sorted as [b(cj3), a(cj5)]. Iters[0]=b at cj3.
	// Advance past cj3: SkipTo(cj4) → b.SkipTo(cj4) → b moves from cj3 to cj7
	// Sort: a(cj5), b(cj7) → Iters[0]=a at cj5 → GetCurEntryID returns cj5
	fc.SkipTo(NewEntryID(NewConjID(4, 0, 1), true))
	if got := fc.GetCurEntryID(); got != NewEntryID(NewConjID(5, 0, 1), true) {
		t.Errorf("Expected entry for conjID=5")
	}

	// Advance past cj5: SkipTo(cj6) → a.SkipTo(cj6) → a moves from cj5 to... wait, a has [cj1, cj5], after cj5 it reaches NULL
	// Wait, a has [cj1, cj5]. After SkipTo(6), a is at NULL.
	// Sort: b(cj7), a(NULL) → Iters[0]=b at cj7 → GetCurEntryID returns cj7
	fc.SkipTo(NewEntryID(NewConjID(6, 0, 1), true))
	if got := fc.GetCurEntryID(); got != NewEntryID(NewConjID(7, 0, 1), true) {
		t.Errorf("Expected entry for conjID=7")
	}
}

func TestFieldCursor_Empty(t *testing.T) {
	fc := NewFieldCursor()
	if !fc.GetCurEntryID().IsNULLEntry() {
		t.Error("Empty FieldCursor should return NULLENTRY")
	}
	if !fc.SkipTo(NewEntryID(NewConjID(1, 0, 1), true)).IsNULLEntry() {
		t.Error("Empty FieldCursor SkipTo should return NULLENTRY")
	}
}

func TestFieldCursors_Heap(t *testing.T) {
	fc1 := NewFieldCursor(NewSliceIterator(NewTerm("a", 0), []EntryID{
		NewEntryID(NewConjID(10, 0, 1), true),
	}))
	fc2 := NewFieldCursor(NewSliceIterator(NewTerm("b", 0), []EntryID{
		NewEntryID(NewConjID(5, 0, 1), true),
	}))

	fcs := NewFieldCursors(2)
	fcs.Append(fc1)
	fcs.Append(fc2)
	fcs.Sort()
	if fcs.Peek() != NewEntryID(NewConjID(5, 0, 1), true) {
		t.Error("Heap FieldCursors: smallest should be first")
	}
	if fcs.Len() != 2 {
		t.Error("Should have 2 elements")
	}
}

// ---------------------------------------------------------------------------
// Document & Conjunction
// ---------------------------------------------------------------------------

func TestDocument_NewDocument(t *testing.T) {
	doc := NewDocument(42)
	if doc.ID != 42 {
		t.Errorf("Doc ID should be 42, got %d", doc.ID)
	}
	if len(doc.Cons) != 0 {
		t.Errorf("New doc should have 0 conjunctions")
	}
}
func TestConjunction_Builders(t *testing.T) {
	conj := NewConjunction()
	conj.In("age", 25)
	conj.In("city", "sh")

	if conj.PredicateCount() != 2 {
		t.Errorf("Should have 2 predicates, got %d", conj.PredicateCount())
	}

	if size := conj.CalcConjSize(); size != 2 {
		t.Errorf("ConjSize should be 2 (both Include), got %d", size)
	}
}

func TestConjunction_InAndNotIn(t *testing.T) {
	conj := NewConjunction()
	conj.In("age", 25).NotIn("city", "bj")

	// Verify
	if size := conj.CalcConjSize(); size != 1 {
		t.Errorf("ConjSize=1 (only 1 Include), got %d", size)
	}
}

func TestConjunction_Exclude(t *testing.T) {
	conj := NewConjunction()
	conj.Exclude("age", 30) // equivalent to NotIn
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate for age")
	}
	if preds[0].Incl != false {
		t.Error("Exclude should set Incl=false")
	}
}

func TestConjunction_Include(t *testing.T) {
	conj := NewConjunction()
	conj.Include("tag", "vip")
	preds := conj.Predicates["tag"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate for tag")
	}
	if preds[0].Incl != true {
		t.Error("Include should set Incl=true")
	}
}

func TestConjunction_GreaterThan(t *testing.T) {
	conj := NewConjunction()
	conj.GreaterThan("age", 18)
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate for age")
	}
	if preds[0].Operator != ValueOptGT {
		t.Errorf("Operator should be GT, got %v", preds[0].Operator)
	}
	if preds[0].Incl != true {
		t.Error("GreaterThan should be Include")
	}
}

func TestConjunction_LessThan(t *testing.T) {
	conj := NewConjunction()
	conj.LessThan("age", 60)
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate for age")
	}
	if preds[0].Operator != ValueOptLT {
		t.Errorf("Operator should be LT, got %v", preds[0].Operator)
	}

}
func TestConjunction_Between(t *testing.T) {
	conj := NewConjunction()
	conj.Between("age", 18, 60)
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Between should create 1 predicate, got %d", len(preds))
	}
	if preds[0].Operator != ValueOptBetween {
		t.Errorf("Operator should be Between")
	}
	if preds[0].Incl != true {
		t.Error("Between should be Include")
	}
}
func TestConjunction_MultipleValues(t *testing.T) {
	conj := NewConjunction()
	conj.In("age", NewIntValues(18, 25, 30))
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate for age")
	}
}

func TestConjunction_SameFieldAddedTwice(t *testing.T) {
	conj := NewConjunction()
	conj.In("age", 18)

	// Adding the same field again should be OK when one is Include and one is Exclude
	conj.NotIn("age", 25)

	if conj.PredicateCount() != 1 {
		t.Errorf("Should still have 1 field entry, got %d", conj.PredicateCount())
	}
	preds := conj.Predicates["age"]
	if len(preds) != 2 {
		t.Fatalf("Should have 2 ValueExprs for age, got %d", len(preds))
	}
}

func TestConjunction_CalcConjSize_ExcludeOnly(t *testing.T) {
	conj := NewConjunction()
	conj.NotIn("age", 18)
	conj.NotIn("city", "bj")

	if size := conj.CalcConjSize(); size != 0 {
		t.Errorf("Pure exclude conjunction should have K=0, got %d", size)
	}
}

func TestConjunction_CalcConjSize_Mixed(t *testing.T) {
	conj := NewConjunction()
	conj.In("age", 25)       // include → counts
	conj.NotIn("city", "bj") // exclude → doesn't count
	conj.In("tag", "vip")    // include → counts

	if size := conj.CalcConjSize(); size != 2 {
		t.Errorf("Should have K=2, got %d", size)
	}
}

func TestDocument_JSONString(t *testing.T) {
	doc := NewDocument(1)
	doc.AddConjunction(NewConjunction().In("age", 25))
	s := doc.JSONString()
	if len(s) == 0 {
		t.Error("JSONString should not be empty")
	}
}

func TestDocument_String(t *testing.T) {
	doc := NewDocument(1)
	doc.AddConjunction(NewConjunction().In("age", 25))
	s := doc.String()
	if len(s) == 0 {
		t.Error("String should not be empty")
	}
}

func TestConjunction_String(t *testing.T) {
	conj := NewConjunction().In("age", 25)
	s := conj.String()
	if len(s) == 0 {
		t.Error("String should not be empty")
	}
}

func TestDocIDList_Contain(t *testing.T) {
	list := DocIDList{1, 3, 5, 7}
	if !list.Contains(3) {
		t.Error("Should contain 3")
	}
	if list.Contains(4) {
		t.Error("Should not contain 4")
	}
}

func TestDocIDList_Sub(t *testing.T) {
	list := DocIDList{1, 3, 5, 7, 9, 11}
	other := DocIDList{3, 7, 11}
	result := list.Sub(other)
	expected := DocIDList{1, 5, 9}
	if len(result) != len(expected) {
		t.Fatalf("Sub result length mismatch: got %v want %v", result, expected)
	}
	for i := range result {
		if result[i] != expected[i] {
			t.Fatalf("Sub result mismatch: got %v want %v", result, expected)
		}
	}
}

func TestDocIDList_Sort(t *testing.T) {
	list := DocIDList{3, 1, 2}
	sort.Sort(list)
	if list[0] != 1 || list[1] != 2 || list[2] != 3 {
		t.Error("Sort failed")
	}
}

// ---------------------------------------------------------------------------
// RetrieveContext & IndexOpt
// ---------------------------------------------------------------------------

func TestNewRetrieveCtx(t *testing.T) {
	ass := Assignments{"age": 25}
	ctx := NewRetrieveCtx(ass)

	_ = ctx
	if ctx.DumpStepInfo {
		t.Error("DumpStepInfo should default to false")
	}
	if ctx.Collector != nil {
		t.Error("Collector should be nil by default")
	}
}

func TestRetrieveCtx_WithObserver(t *testing.T) {
	ass := Assignments{"age": 25}
	obs := &testObserver{}
	ctx := NewRetrieveCtx(ass, func(c *RetrieveContext) {
		c.Observer = obs
	})
	if ctx.Observer != obs {
		t.Error("Observer should be set via IndexOpt")
	}
}

type testObserver struct {
	startCount int
	endCount   int
	matchCount int
	exclCount  int
	cursorInit int
}

func (o *testObserver) OnRetrieveStart(ctx *RetrieveContext) { o.startCount++ }
func (o *testObserver) OnRetrieveEnd(ctx *RetrieveContext)   { o.endCount++ }
func (o *testObserver) OnMatch(docID DocID, conjID ConjID)   { o.matchCount++ }
func (o *testObserver) OnExcludeSkip(docID DocID)            { o.exclCount++ }
func (o *testObserver) OnCursorInit(fieldCount int)          { o.cursorInit++ }

// ---------------------------------------------------------------------------
// Schema & FieldOption
// ---------------------------------------------------------------------------

func TestFieldOption(t *testing.T) {
	opts := FieldOption{IndexType: IndexNameDefault, Encoder: "number"}
	if opts.IndexType != IndexNameDefault {
		t.Error("IndexType should be IndexNameDefault")
	}
	if opts.Encoder != "number" {
		t.Error("Encoder should be number")
	}
}

func TestSchema(t *testing.T) {
	schema := Schema{"age": {Encoder: "number"}}
	if schema["age"].Encoder != "number" {
		t.Errorf("Schema not correctly initialized: %+v", schema)
	}
}

// ---------------------------------------------------------------------------
// ValueExpr & Predicate
// ---------------------------------------------------------------------------

func TestNewValueExpr(t *testing.T) {
	expr := NewValueExpr(ValueOptEQ, 42, true)
	if expr.Value != 42 {
		t.Errorf("Value should be 42")
	}
	if expr.Incl != true {
		t.Error("Incl should be true")
	}
	if expr.Operator != ValueOptEQ {
		t.Error("Operator should be EQ")
	}
}

func TestNewPredicate(t *testing.T) {
	p := NewPredicate("age", true, 25)
	if p.Field != "age" {
		t.Errorf("Field should be 'age'")
	}
	if p.Value != 25 {
		t.Errorf("Value should be 25")
	}
	if p.Incl != true {
		t.Error("Include should be true")
	}
}

func TestAssignments_Size(t *testing.T) {
	ass := Assignments{
		"age":  25,
		"city": "bj",
	}
	if size := ass.Size(); size != 2 {
		t.Errorf("Size should be 2, got %d", size)
	}
}

func TestTerm_String(t *testing.T) {
	t1 := NewTerm("age", 25)
	s1 := t1.String()
	if len(s1) == 0 {
		t.Error("Term.String for int should not be empty")
	}

	t2 := NewTerm("city", "bj")
	s2 := t2.String()
	if len(s2) == 0 {
		t.Error("Term.String for string should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Write/Read roundtrip for various types
// ---------------------------------------------------------------------------

func TestValueExpr_JSONString(t *testing.T) {
	expr := NewValueExpr(ValueOptEQ, 25, true)
	s := expr.JSONString()
	if len(s) == 0 {
		t.Error("JSONString should not be empty")
	}
}

func TestValueExpr_String(t *testing.T) {
	expr := NewValueExpr(ValueOptEQ, 25, true)
	s := expr.String()
	if len(s) == 0 {
		t.Error("String should not be empty")
	}
}

func TestNewIntValues(t *testing.T) {
	v := NewIntValues(1, 2, 3)
	iv, ok := v.([]int)
	if !ok || len(iv) != 3 || iv[0] != 1 {
		t.Errorf("NewIntValues returned unexpected: %v", v)
	}
}

func TestNewInt32Values(t *testing.T) {
	v := NewInt32Values(1, 2)
	iv, ok := v.([]int32)
	if !ok || len(iv) != 2 {
		t.Errorf("NewInt32Values returned unexpected: %v", v)
	}
}

func TestNewInt64Values(t *testing.T) {
	v := NewInt64Values(100, 200)
	iv, ok := v.([]int64)
	if !ok || len(iv) != 2 {
		t.Errorf("NewInt64Values returned unexpected: %v", v)
	}
}

func TestNewStrValues(t *testing.T) {
	v := NewStrValues("a", "b", "c")
	sv, ok := v.([]string)
	if !ok || len(sv) != 3 || sv[0] != "a" {
		t.Errorf("NewStrValues returned unexpected: %v", v)
	}
}

func TestNewPredicateWithExpr(t *testing.T) {
	expr := NewValueExpr(ValueOptGT, 18, true)
	p := NewPredicateWithExpr("age", expr)
	if p.Field != "age" || p.Operator != ValueOptGT {
		t.Errorf("NewPredicate2 not correct: %+v", p)
	}
}

func TestNewGTValueExpr(t *testing.T) {
	expr := NewGTValueExpr(18)
	if expr.Operator != ValueOptGT || expr.Incl != true || expr.Value != int64(18) {
		t.Errorf("NewGTValueExpr not correct: %+v", expr)
	}
}

func TestNewLTValueExpr(t *testing.T) {
	expr := NewLTValueExpr(60)
	if expr.Operator != ValueOptLT || expr.Incl != true || expr.Value != int64(60) {
		t.Errorf("NewLTValueExpr not correct: %+v", expr)
	}
}

func TestValueExpr_BooleanToken(t *testing.T) {
	incl := NewValueExpr(ValueOptEQ, 1, true)
	if tok := incl.BooleanToken(); tok != "in" {
		t.Errorf("Include token should be 'in', got '%s'", tok)
	}

	excl := NewValueExpr(ValueOptEQ, 1, false)
	if tok := excl.BooleanToken(); tok != "not" {
		t.Errorf("Exclude token should be 'not', got '%s'", tok)
	}
}
func TestValueExpr_OperatorName(t *testing.T) {
	// OperatorName returns "" for EQ (default case)
	eq := NewValueExpr(ValueOptEQ, 1, true)
	if name := eq.OperatorName(); name != "" {
		t.Errorf("EQ operator name should be '', got '%s'", name)
	}

	gt := NewValueExpr(ValueOptGT, 1, true)
	if name := gt.OperatorName(); name != ">" {
		t.Errorf("GT operator name should be '>', got '%s'", name)
	}

	lt := NewValueExpr(ValueOptLT, 1, true)
	if name := lt.OperatorName(); name != "<" {
		t.Errorf("LT operator name should be '<', got '%s'", name)
	}
}

// ---------------------------------------------------------------------------
// Entries helper
// ---------------------------------------------------------------------------

func TestEntries_LenSwapLess(t *testing.T) {
	cj1 := NewConjID(1, 0, 0)
	cj2 := NewConjID(2, 0, 0)
	e := Entries{
		NewEntryID(cj2, true),
		NewEntryID(cj1, true),
	}
	if e.Len() != 2 {
		t.Errorf("Len should be 2")
	}
	if e.Less(0, 1) {
		t.Errorf("element 0 should be > element 1")
	}
	e.Swap(0, 1)
	if !e.Less(0, 1) {
		t.Errorf("After swap, element 0 should be < element 1")
	}
}

// ---------------------------------------------------------------------------
// Bit-level entry tests
// ---------------------------------------------------------------------------

func TestConjID_BitLayout(t *testing.T) {
	// Verify size occupies bits [59:52] (8 bits)
	cj := NewConjID(1, 0, 0xFF) // size = 255
	if cj.Size() != 255 {
		t.Errorf("Size should be 255, got %d", cj.Size())
	}

	// Verify index occupies bits [51:44] (8 bits)
	cj = NewConjID(1, 0xFF, 0)
	if cj.Index() != 255 {
		t.Errorf("Index should be 255, got %d", cj.Index())
	}
}

func TestConjID_SortedByEntryID(t *testing.T) {
	// Verify that EntryIDs with same ConjID sort correctly by include/exclude bit
	cj := NewConjID(1, 0, 1)
	incl := NewEntryID(cj, true)
	excl := NewEntryID(cj, false)

	// The lowest bit determines include/exclude
	// excl should be < incl
	if excl >= incl {
		t.Error("Exclude entry should be less than Include entry for the same ConjID")
	}

	// GetConjID should strip the include/exclude bit
	if incl.GetConjID() != excl.GetConjID() {
		t.Error("GetConjID should be the same for both entries")
	}
}

type testResultCollector struct {
	ids []DocID
}

func (c *testResultCollector) Add(id DocID) {
	c.ids = append(c.ids, id)
}

func bitmapToSlice(b *BitmapDocSet) DocIDList {
	if b == nil {
		return nil
	}
	var ids DocIDList
	b.ForEach(func(id DocID) {
		ids = append(ids, id)
	})
	return ids
}

// ---------------------------------------------------------------------------
// LiveDocs serialization edge cases
// ---------------------------------------------------------------------------

func TestLiveDocs_DeserializeEmpty(t *testing.T) {
	// Invalid data should fail
	ld := NewLiveDocs()
	if err := ld.Deserialize([]byte{0, 1, 2}); err == nil {
		t.Error("Should fail on invalid data")
	}
}

func TestLiveDocs_SerializeDeserialize_Empty(t *testing.T) {
	ld := NewLiveDocs()
	data, err := ld.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	ld2 := NewLiveDocs()
	if err := ld2.Deserialize(data); err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}
	if ld2.DeletedCount() != 0 {
		t.Errorf("Empty LiveDocs should have 0 deletions")
	}
}

func TestValueExpr_OperatorNameAll(t *testing.T) {

	ops := []struct {
		op   ValueOpt
		want string
	}{
		{ValueOptEQ, ""},
		{ValueOptGT, ">"},
		{ValueOptLT, "<"},
		{ValueOptBetween, "between"},
	}
	for _, tc := range ops {
		expr := ValueExpr{Operator: tc.op}
		if name := expr.OperatorName(); name != tc.want {
			t.Errorf("OperatorName(%d) = '%s', want '%s'", tc.op, name, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DocIDCollector edge cases
// ---------------------------------------------------------------------------

func TestDocIDCollector_ResetThenReuse(t *testing.T) {
	c := NewDocIDCollector()
	c.Add(1)
	c.Add(2)
	c.Reset()

	c.Add(3)
	if c.DocCount() != 1 {
		t.Errorf("After reset and re-add, count should be 1, got %d", c.DocCount())
	}

	ids := bitmapToSlice(c.Bitmap())
	if len(ids) != 1 || ids[0] != 3 {
		t.Errorf("Unexpected ids after reset: %v", ids)
	}
}

func TestFieldCursor_NilIterators(t *testing.T) {
	fc := NewFieldCursor(nil)
	if !fc.GetCurEntryID().IsNULLEntry() {
		t.Error("Nil iterator should produce NULLENTRY")
	}
}

// ---------------------------------------------------------------------------
// WildcardTerm
// ---------------------------------------------------------------------------

func TestWildcardTerm(t *testing.T) {
	if WildcardTerm.Field != WildcardFieldName {
		t.Errorf("WildcardTerm field should be '%s'", WildcardFieldName)
	}
}

func TestConjunction_JSONString(t *testing.T) {
	conj := NewConjunction().In("age", 25)
	s := conj.JSONString()
	if len(s) == 0 {
		t.Error("JSONString should not be empty")
	}
}

// ---------------------------------------------------------------------------
// Ensure whole-file export functions produce valid roundtrips
// ---------------------------------------------------------------------------

func TestDocument_AddConjunctionsMulti(t *testing.T) {
	doc := NewDocument(1)
	c1 := NewConjunction().In("age", 18)
	c2 := NewConjunction().In("city", "bj")
	doc.AddConjunctions(c1, c2)
	if len(doc.Cons) != 2 {
		t.Errorf("Should have 2 conjunctions, got %d", len(doc.Cons))
	}
}

func TestConjunction_AddPredicates(t *testing.T) {
	p1 := NewPredicate("age", true, 25)
	p2 := NewPredicate("city", true, "bj")
	conj := NewConjunction().AddPredicates(p1, p2)
	if conj.PredicateCount() != 2 {
		t.Errorf("Should have 2 predicate fields, got %d", conj.PredicateCount())
	}
}

func TestConjunction_AddPredicateValues(t *testing.T) {
	conj := NewConjunction().AddPredicateValues("age", true, NewIntValues(18, 25))
	preds := conj.Predicates["age"]
	if len(preds) != 1 {
		t.Fatalf("Should have 1 predicate, got %d", len(preds))
	}
	if preds[0].Incl != true {
		t.Error("Include should be true")
	}
}

func TestFieldCursor_DumpInfo(t *testing.T) {
	it := NewSliceIterator(NewTerm("x", 0), []EntryID{
		NewEntryID(NewConjID(1, 0, 1), true),
	})
	fc := NewFieldCursor(it)
	info := fc.DumpInfo()
	if len(info) != 1 {
		t.Errorf("DumpInfo should return 1 entry, got %d", len(info))
	}
}

func TestIndexerSettings(t *testing.T) {
	s := IndexerSettings{
		FieldConfig: map[BEField]FieldOption{
			"age": {Encoder: "number"},
		},
	}
	if len(s.FieldConfig) != 1 {
		t.Errorf("IndexerSettings should have 1 field config")
	}
}

// ---------------------------------------------------------------------------
// Confirm errors are non-nil sentinels
// ---------------------------------------------------------------------------

func TestSentinelErrors(t *testing.T) {
	if ErrFieldNotConfigured == nil {
		t.Error("ErrFieldNotConfigured should be non-nil")
	}
	if ErrUnknownQueryField == nil {
		t.Error("ErrUnknownQueryField should be non-nil")
	}
	if ErrFieldIndexMissing == nil {
		t.Error("ErrFieldIndexMissing should be non-nil")
	}
	if ErrUnsupportedPredicate == nil {
		t.Error("ErrUnsupportedPredicate should be non-nil")
	}
}

// ---------------------------------------------------------------------------
// SliceIterator edge cases
// ---------------------------------------------------------------------------

func TestSliceIterator_SkipToEmpty(t *testing.T) {
	it := NewSliceIterator(NewTerm("x", 0), nil)
	if !it.Current().IsNULLEntry() {
		t.Error("Empty iterator should return NULLENTRY")
	}
	got := it.SkipTo(NewEntryID(NewConjID(1, 0, 0), true))
	if !got.IsNULLEntry() {
		t.Error("SkipTo on empty iterator should return NULLENTRY")
	}
}

func TestSliceIterator_DuplicateEntries(t *testing.T) {
	cj := NewConjID(5, 0, 0)
	entries := []EntryID{
		NewEntryID(cj, false),
		NewEntryID(cj, true),
	}
	it := NewSliceIterator(NewTerm("x", 0), entries)
	if it.Current() != entries[0] {
		t.Error("Should start at first entry")
	}
	// SkipTo the include entry (second)
	got := it.SkipTo(entries[1])
	if got != entries[1] {
		t.Errorf("Should skip to include entry: got=%d want=%d", got, entries[1])
	}
}

// ---------------------------------------------------------------------------
// Pointer to slice in common tests
// ---------------------------------------------------------------------------

func TestDocIDCollector_GetDocIDsIntoAppend(t *testing.T) {
	c := NewDocIDCollector()
	c.Add(1)
	c.Add(2)

	into := DocIDList{0} // pre-populated
	into = append(into, bitmapToSlice(c.Bitmap())...)
	if len(into) != 3 || into[0] != 0 || into[1] != 1 || into[2] != 2 {
		t.Errorf("Unexpected appended result: %v", into)
	}
}

// ---------------------------------------------------------------------------
// Verify buffer pool recycles cleanly
// ---------------------------------------------------------------------------

func TestDocIDCollector_PutAfterNil(t *testing.T) {
	// Should not panic
	PutCollector(nil)
	PutCollector(PickCollector())
}

// ---------------------------------------------------------------------------
// Straight-line doc building
// ---------------------------------------------------------------------------

func TestNewDocumentFactory(t *testing.T) {
	doc := NewDocument(0)
	if doc == nil {
		t.Fatal("NewDocument should not return nil")
	}
	if doc.ID != 0 {
		t.Errorf("Doc ID should be 0")
	}
	if doc.Cons == nil {
		t.Errorf("Cons should be initialized to empty slice")
	}
}

// ---------------------------------------------------------------------------
// FieldCursor handles mixed nil/non-nil iterators
// ---------------------------------------------------------------------------

func TestFieldCursor_FiltersNilIterators(t *testing.T) {
	realIter := NewSliceIterator(NewTerm("x", 0), []EntryID{
		NewEntryID(NewConjID(1, 0, 0), true),
	})
	fc := NewFieldCursor(nil, realIter)
	if fc.GetCurEntryID() != NewEntryID(NewConjID(1, 0, 0), true) {
		t.Error("Nil iterators should be filtered out")
	}
	if len(fc.Iters) != 1 {
		t.Errorf("Should have 1 iterator, got %d", len(fc.Iters))
	}
}

// ---------------------------------------------------------------------------
// Edge: empty document
// ---------------------------------------------------------------------------

func TestNewDocument_Empty(t *testing.T) {
	doc := NewDocument(0)
	if doc.Cons == nil {
		t.Error("NewDocument should initialize empty Cons slice")
	}
}

// Ensure bytes.Buffer roundtrip compiles
var _ = bytes.NewBuffer

// ---------------------------------------------------------------------------
// Phase 1: K-Group Elimination — KStartEntryID
// ---------------------------------------------------------------------------

func TestKStartEntryID(t *testing.T) {
	// K=0: EntryID has K=0 in bits 56-63 → uint64(0)<<56 = 0
	if KStartEntryID(0) != 0 {
		t.Errorf("KStartEntryID(0) should be 0, got %d", KStartEntryID(0))
	}
	// K=1: EntryID has K=1 in bits 56-63 → uint64(1)<<56
	expected := EntryID(uint64(1) << 56)
	if KStartEntryID(1) != expected {
		t.Errorf("KStartEntryID(1) should be %d, got %d", expected, KStartEntryID(1))
	}
	// K=255: maximum
	expected = EntryID(uint64(255) << 56)
	if KStartEntryID(255) != expected {
		t.Errorf("KStartEntryID(255) should be %d, got %d", expected, KStartEntryID(255))
	}
	// K boundary: [KStartEntryID(k), KStartEntryID(k+1)) should not overlap
	for k := 0; k < 255; k++ {
		if KStartEntryID(k) >= KStartEntryID(k+1) {
			t.Errorf("KStartEntryID(%d) >= KStartEntryID(%d)", k, k+1)
		}
	}
}

// Verify KStartEntryID correctly decomposes EntryIDs by K.
func TestKStartEntryID_RangePartition(t *testing.T) {
	for k := 0; k < 100; k++ {
		for incl := 0; incl < 2; incl++ {
			eid := NewEntryID(NewConjID(100, 0, k), incl == 1)
			if eid < KStartEntryID(k) {
				t.Errorf("K=%d entry %d should be >= KStartEntryID(%d)=%d", k, eid, k, KStartEntryID(k))
			}
			if eid >= KStartEntryID(k+1) {
				t.Errorf("K=%d entry %d should be < KStartEntryID(%d)=%d", k, eid, k+1, KStartEntryID(k+1))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 1: K-Group Elimination — ReachEnd & CompactLast
// ---------------------------------------------------------------------------

func TestSliceIterator_ReachEnd(t *testing.T) {
	eids := []EntryID{
		NewEntryID(NewConjID(1, 0, 1), true),
		NewEntryID(NewConjID(3, 0, 1), true),
	}
	it := NewSliceIterator(NewTerm("x", 0), eids)

	if it.ReachEnd() {
		t.Error("Fresh iterator should not have reached end")
	}

	it.SkipTo(NewEntryID(NewConjID(10, 0, 1), true))
	if !it.ReachEnd() {
		t.Error("After skipping past all entries, should have reached end")
	}

	// Empty iterator
	empty := NewSliceIterator(NewTerm("e", 0), nil)
	if !empty.ReachEnd() {
		t.Error("Empty iterator should report reach end")
	}
}

func TestFieldCursor_ReachEnd(t *testing.T) {
	eids := []EntryID{NewEntryID(NewConjID(1, 0, 1), true)}
	it := NewSliceIterator(NewTerm("x", 0), eids)
	fc := NewFieldCursor(it)

	if fc.ReachEnd() {
		t.Error("Fresh cursor should not have reached end")
	}

	fc.SkipTo(NewEntryID(NewConjID(10, 0, 1), true))
	if !fc.ReachEnd() {
		t.Error("After skipping past all entries, cursor should have reached end")
	}

	empty := NewFieldCursor()
	if !empty.ReachEnd() {
		t.Error("Empty cursor should report reach end")
	}
}

func TestFieldCursors_CompactLast(t *testing.T) {
	eids1 := []EntryID{NewEntryID(NewConjID(1, 0, 1), true)}
	eids2 := []EntryID{NewEntryID(NewConjID(5, 0, 1), true)}

	fc1 := NewFieldCursor(NewSliceIterator(NewTerm("a", 0), eids1))
	fc2 := NewFieldCursor(NewSliceIterator(NewTerm("b", 0), eids2))

	fcs := NewFieldCursors(2)
	fcs.Append(fc1)
	fcs.Append(fc2)

	// Both cursors alive
	fcs.CompactLast()
	if fcs.Len() != 2 {
		t.Errorf("CompactLast should keep both cursors, got %d", fcs.Len())
	}

	// Exhaust fc1
	fc1.SkipTo(NewEntryID(NewConjID(10, 0, 1), true))
	// Rebuild fc1 into fcs
	fcs2 := NewFieldCursors(2)
	fcs2.Append(fc1)
	fcs2.Append(fc2)
	fcs2.Sort()
	fcs2.CompactLast()
	if fcs2.Len() != 1 {
		t.Errorf("CompactLast should remove exhausted fc1, got %d", fcs2.Len())
	}
	// Remaining cursor should be fc2
	if fcs2.Peek() != eids2[0] {
		t.Errorf("Remaining cursor should be fc2")
	}
}

func TestFieldCursors_CompactLast_AllExhausted(t *testing.T) {
	eids := []EntryID{NewEntryID(NewConjID(1, 0, 1), true)}
	fc := NewFieldCursor(NewSliceIterator(NewTerm("x", 0), eids))
	fc.SkipTo(NewEntryID(NewConjID(10, 0, 1), true))

	fcs := NewFieldCursors(1)
	fcs.Append(fc)
	fcs.Sort()
	fcs.CompactLast()

	if fcs.Len() != 0 {
		t.Error("CompactLast should clear all exhausted cursors")
	}
}

// ---------------------------------------------------------------------------
// id_bounds external test
// ---------------------------------------------------------------------------

var _ = NewTerm

func TestConjID_KAtByteBoundary(t *testing.T) {
	// Verify K occupies exactly the top byte of EntryID (bits 56-63),
	// which means sorting by EntryID naturally groups same-K entries.
	for k := 0; k <= 255; k++ {
		for docID := DocID(1); docID < 5; docID++ {
			conj := NewConjID(docID, 0, k)
			eid := NewEntryID(conj, true)
			gotK := int((uint64(eid) >> 56) & 0xFF)
			if gotK != k {
				t.Errorf("K mismatch for K=%d docID=%d: got K=%d from EntryID", k, docID, gotK)
			}
		}
	}
}
