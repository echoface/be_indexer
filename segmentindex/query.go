package segmentindex

import (
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/RoaringBitmap/roaring/roaring64"
	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/storage"
)

// queryFieldValue is the normalized form of one query field. Values are
// converted once per query instead of once per conjunction group.
type queryFieldValue struct {
	field   be.BEField
	index   *queryFieldIndex
	values  []string
	numbers []int64
	// numberStorage keeps short numeric query slices inside the prepared
	// query object. This avoids allocating one temporary slice per numeric
	// field for the common one-value query case.
	numberStorage [16]int64
	// numeric marks values supplied as numeric Go values. Exact lookups use
	// the canonical int64 posting map and avoid converting every value to a
	// string; string queries retain the original string-key semantics.
	numeric bool
}

type preparedQuery struct {
	fields []queryFieldValue
}

var preparedQueryFieldPool = sync.Pool{
	New: func() interface{} {
		return make([]queryFieldValue, 0, 16)
	},
}

// queryScratch owns the mutable bitmaps used while evaluating one query. The
// index is immutable and safe for concurrent readers; keeping these bitmaps
// per-query avoids cloning large group sets on every retrieval.
type queryScratch struct {
	candidate   *roaring64.Bitmap
	includeHits *roaring64.Bitmap
	excludeHits *roaring64.Bitmap
	missing     *roaring64.Bitmap
	slots       *roaring64.Bitmap
}

var queryScratchPool = sync.Pool{
	New: func() interface{} {
		return &queryScratch{
			candidate:   roaring64.New(),
			includeHits: roaring64.New(),
			excludeHits: roaring64.New(),
			missing:     roaring64.New(),
			slots:       roaring64.New(),
		}
	},
}

func acquireQueryScratch() *queryScratch {
	return queryScratchPool.Get().(*queryScratch)
}

func releaseQueryScratch(scratch *queryScratch) {
	if scratch == nil {
		return
	}
	scratch.candidate.Clear()
	scratch.includeHits.Clear()
	scratch.excludeHits.Clear()
	scratch.missing.Clear()
	scratch.slots.Clear()
	queryScratchPool.Put(scratch)
}

// queryPosting is a compact posting over dense query slots. Most immutable
// groups contain one document, so the common case stores one slot without
// allocating a bitmap or slice.
type queryPosting struct {
	single uint64
	slots  []uint64
	count  int
}

func (p *queryPosting) add(slot uint64) {
	switch p.count {
	case 0:
		p.single = slot
	case 1:
		p.slots = []uint64{p.single, slot}
	default:
		p.slots = append(p.slots, slot)
	}
	p.count++
}

func (p queryPosting) addHits(dst *roaring64.Bitmap) {
	switch p.count {
	case 0:
	case 1:
		dst.Add(p.single)
	default:
		for _, slot := range p.slots {
			dst.Add(slot)
		}
	}
}

func (p queryPosting) contains(slot uint64) bool {
	switch p.count {
	case 0:
		return false
	case 1:
		return p.single == slot
	default:
		idx := sort.Search(len(p.slots), func(i int) bool { return p.slots[i] >= slot })
		return idx < len(p.slots) && p.slots[idx] == slot
	}
}

func (p queryPosting) appendTo(dst []uint64) []uint64 {
	switch p.count {
	case 0:
	case 1:
		dst = append(dst, p.single)
	default:
		dst = append(dst, p.slots...)
	}
	return dst
}

type queryRangeEntry struct {
	left      int64
	right     int64
	slotStart uint64
	slotEnd   uint64
}

type queryRangeNode struct {
	start    int
	end      int
	maxRight int64
	left     int
	right    int
}

// queryRangeIndex indexes half-open [left,right) predicates by their bounds.
// Unit intervals are kept in a point map for the common exact-value range
// case. Other intervals use a static max-right tree so a point query does not
// walk every earlier interval.
type queryRangeIndex struct {
	entries []queryRangeEntry
	points  map[int64]*roaring64.Bitmap
	nodes   []queryRangeNode
	root    int
}

func (x *queryRangeIndex) add(left, right int64, slotStart, slotEnd uint64) {
	if slotStart >= slotEnd || left >= right {
		return
	}
	if left != math.MaxInt64 && right == left+1 {
		if x.points == nil {
			x.points = make(map[int64]*roaring64.Bitmap)
		}
		hits := x.points[left]
		if hits == nil {
			hits = roaring64.New()
			x.points[left] = hits
		}
		hits.AddRange(slotStart, slotEnd)
		return
	}
	x.entries = append(x.entries, queryRangeEntry{
		left:      left,
		right:     right,
		slotStart: slotStart,
		slotEnd:   slotEnd,
	})
}

func (x *queryRangeIndex) finish() {
	sort.Slice(x.entries, func(i, j int) bool {
		if x.entries[i].left == x.entries[j].left {
			if x.entries[i].right == x.entries[j].right {
				if x.entries[i].slotStart == x.entries[j].slotStart {
					return x.entries[i].slotEnd < x.entries[j].slotEnd
				}
				return x.entries[i].slotStart < x.entries[j].slotStart
			}
			return x.entries[i].right < x.entries[j].right
		}
		return x.entries[i].left < x.entries[j].left
	})
	x.root = -1
	if len(x.entries) != 0 {
		x.root = x.build(0, len(x.entries))
	}
}

func (x *queryRangeIndex) build(start, end int) int {
	nodeIndex := len(x.nodes)
	x.nodes = append(x.nodes, queryRangeNode{start: start, end: end, left: -1, right: -1})
	maxRight := int64(math.MinInt64)
	for idx := start; idx < end; idx++ {
		if x.entries[idx].right > maxRight {
			maxRight = x.entries[idx].right
		}
	}
	x.nodes[nodeIndex].maxRight = maxRight
	if end-start == 1 {
		return nodeIndex
	}
	mid := start + (end-start)/2
	left := x.build(start, mid)
	right := x.build(mid, end)
	x.nodes[nodeIndex].left = left
	x.nodes[nodeIndex].right = right
	return nodeIndex
}

func (x *queryRangeIndex) addHits(dst *roaring64.Bitmap, numbers []int64) {
	if x == nil || len(numbers) == 0 {
		return
	}
	for _, number := range numbers {
		if hits := x.points[number]; hits != nil {
			dst.Or(hits)
		}
		x.search(x.root, number, dst)
	}
}

func (x *queryRangeIndex) contains(slot uint64, numbers []int64) bool {
	if x == nil || len(numbers) == 0 {
		return false
	}
	for _, number := range numbers {
		if hits := x.points[number]; hits != nil && hits.Contains(slot) {
			return true
		}
		for idx := range x.entries {
			entry := x.entries[idx]
			if entry.left > number {
				break
			}
			if entry.right > number && entry.slotStart <= slot && slot < entry.slotEnd {
				return true
			}
		}
	}
	return false
}

func (x *queryRangeIndex) search(nodeIndex int, number int64, dst *roaring64.Bitmap) {
	if nodeIndex < 0 {
		return
	}
	node := &x.nodes[nodeIndex]
	if node.maxRight <= number || x.entries[node.start].left > number {
		return
	}
	if node.end-node.start == 1 {
		entry := x.entries[node.start]
		if entry.right > number {
			dst.AddRange(entry.slotStart, entry.slotEnd)
		}
		return
	}
	x.search(node.left, number, dst)
	x.search(node.right, number, dst)
}

type queryFieldIndex struct {
	include         map[string]queryPosting
	includeNumbers  map[int64]queryPosting
	exclude         map[string]queryPosting
	excludeNumbers  map[int64]queryPosting
	includeRequired *roaring64.Bitmap
	includeRanges   queryRangeIndex
	excludeRanges   queryRangeIndex

	groupInclude         map[string]queryPosting
	groupIncludeNumbers  map[int64]queryPosting
	groupExclude         map[string]queryPosting
	groupExcludeNumbers  map[int64]queryPosting
	groupIncludeRequired *roaring64.Bitmap
	groupIncludeRanges   queryRangeIndex
	groupExcludeRanges   queryRangeIndex
}

type queryIndex struct {
	all            *roaring64.Bitmap
	allGroups      *roaring64.Bitmap
	single         *roaring64.Bitmap
	allSingle      bool
	fields         map[string]*queryFieldIndex
	slotGroup      []uint64
	slotDocs       []uint64
	groupSlotStart []uint64
	groupSlotEnd   []uint64
}

func newQueryFieldIndex() *queryFieldIndex {
	return &queryFieldIndex{
		include:              make(map[string]queryPosting),
		includeNumbers:       make(map[int64]queryPosting),
		exclude:              make(map[string]queryPosting),
		excludeNumbers:       make(map[int64]queryPosting),
		includeRequired:      roaring64.New(),
		groupInclude:         make(map[string]queryPosting),
		groupIncludeNumbers:  make(map[int64]queryPosting),
		groupExclude:         make(map[string]queryPosting),
		groupExcludeNumbers:  make(map[int64]queryPosting),
		groupIncludeRequired: roaring64.New(),
	}
}

func (q *queryIndex) field(name string) *queryFieldIndex {
	if q.fields == nil {
		q.fields = make(map[string]*queryFieldIndex)
	}
	field := q.fields[name]
	if field == nil {
		field = newQueryFieldIndex()
		q.fields[name] = field
	}
	return field
}

func canonicalInt64(value string) (int64, bool) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != value {
		return 0, false
	}
	return n, true
}

func (f *queryFieldIndex) addTerm(value string, slot uint64, exclude bool) {
	terms := f.include
	numbers := f.includeNumbers
	if exclude {
		terms = f.exclude
		numbers = f.excludeNumbers
	}
	posting := terms[value]
	posting.add(slot)
	terms[value] = posting
	if number, ok := canonicalInt64(value); ok {
		numeric := numbers[number]
		numeric.add(slot)
		numbers[number] = numeric
	}
}

func (f *queryFieldIndex) addGroupTerm(value string, group uint64, exclude bool) {
	terms := f.groupInclude
	numbers := f.groupIncludeNumbers
	if exclude {
		terms = f.groupExclude
		numbers = f.groupExcludeNumbers
	}
	posting := terms[value]
	posting.add(group)
	terms[value] = posting
	if number, ok := canonicalInt64(value); ok {
		numeric := numbers[number]
		numeric.add(group)
		numbers[number] = numeric
	}
}

func (f *queryFieldIndex) addTermHits(dst *roaring64.Bitmap, query queryFieldValue, exclude bool) {
	if query.numeric {
		terms := f.includeNumbers
		if exclude {
			terms = f.excludeNumbers
		}
		for _, value := range query.numbers {
			if posting, ok := terms[value]; ok {
				posting.addHits(dst)
			}
		}
		return
	}
	terms := f.include
	if exclude {
		terms = f.exclude
	}
	for _, value := range query.values {
		if posting, ok := terms[value]; ok {
			posting.addHits(dst)
		}
	}
}

func (f *queryFieldIndex) addGroupTermHits(dst *roaring64.Bitmap, query queryFieldValue, exclude bool) {
	if query.numeric {
		terms := f.groupIncludeNumbers
		if exclude {
			terms = f.groupExcludeNumbers
		}
		for _, value := range query.numbers {
			if posting, ok := terms[value]; ok {
				posting.addHits(dst)
			}
		}
		return
	}
	terms := f.groupInclude
	if exclude {
		terms = f.groupExclude
	}
	for _, value := range query.values {
		if posting, ok := terms[value]; ok {
			posting.addHits(dst)
		}
	}
}

// newQueryIndex builds the immutable query slots used by RetrieveAssignments.
// A slot is a dense (group, doc) pair. This keeps conjunction boundaries
// implicit in the bitmap domain, so values from separate DNF groups can never
// intersect into a false match.
func newQueryIndex(groups []groupMeta, segments []*storage.Segment) *queryIndex {
	q := &queryIndex{
		all:       roaring64.New(),
		allGroups: roaring64.New(),
		single:    roaring64.New(),
		fields:    make(map[string]*queryFieldIndex),
	}
	for groupID, meta := range groups {
		group := uint64(groupID)
		slotStart := uint64(len(q.slotDocs))
		q.groupSlotStart = append(q.groupSlotStart, slotStart)
		slotByDoc := make(map[uint64]uint64, len(meta.docs))
		for offset, doc := range meta.docs {
			slot := slotStart + uint64(offset)
			q.slotGroup = append(q.slotGroup, group)
			q.slotDocs = append(q.slotDocs, doc)
			slotByDoc[doc] = slot
		}
		slotEnd := uint64(len(q.slotDocs))
		q.groupSlotEnd = append(q.groupSlotEnd, slotEnd)
		q.all.AddRange(slotStart, slotEnd)
		q.allGroups.Add(group)
		if len(meta.docs) == 1 {
			q.single.Add(group)
		}

		for field, shape := range meta.shapes {
			index := q.field(field)
			if shape.hasIncl {
				index.includeRequired.AddRange(slotStart, slotEnd)
				index.groupIncludeRequired.Add(group)
			}
		}
		for _, predicate := range meta.ranges {
			index := q.field(predicate.field)
			if predicate.incl {
				index.includeRequired.AddRange(slotStart, slotEnd)
				index.includeRanges.add(predicate.left, predicate.right, slotStart, slotEnd)
				index.groupIncludeRequired.Add(group)
				index.groupIncludeRanges.add(predicate.left, predicate.right, group, group+1)
			} else {
				index.excludeRanges.add(predicate.left, predicate.right, slotStart, slotEnd)
				index.groupExcludeRanges.add(predicate.left, predicate.right, group, group+1)
			}
		}

		if groupID >= len(segments) {
			continue
		}
		segment := segments[groupID]
		for termID, key := range segment.TermKeys() {
			if len(key.Value) == 0 {
				continue
			}
			prefix := key.Value[0]
			if prefix != includeValuePrefix[0] && prefix != excludeValuePrefix[0] {
				continue
			}
			posting, err := segment.Posting(storage.TermID(termID))
			if err != nil {
				continue
			}
			index := q.field(key.Field)
			index.addGroupTerm(key.Value[1:], group, prefix == excludeValuePrefix[0])
			for _, doc := range posting {
				slot, ok := slotByDoc[doc]
				if !ok {
					continue
				}
				index.addTerm(key.Value[1:], slot, prefix == excludeValuePrefix[0])
			}
		}
	}
	for _, field := range q.fields {
		field.includeRanges.finish()
		field.excludeRanges.finish()
		field.groupIncludeRanges.finish()
		field.groupExcludeRanges.finish()
	}
	q.allSingle = len(groups) > 0 && q.single.GetCardinality() == uint64(len(groups))
	return q
}

// numericQueryValues recognizes numeric-only query values without going
// through reflection or string conversion. It returns false for strings and
// mixed values so their original exact-string semantics remain unchanged.
func numericQueryValues(raw be.Values, storage *[16]int64) ([]int64, bool) {
	switch values := raw.(type) {
	case int:
		storage[0] = int64(values)
		return storage[:1], true
	case int8:
		storage[0] = int64(values)
		return storage[:1], true
	case int16:
		storage[0] = int64(values)
		return storage[:1], true
	case int32:
		storage[0] = int64(values)
		return storage[:1], true
	case int64:
		storage[0] = values
		return storage[:1], true
	case uint:
		if uint64(values) <= math.MaxInt64 {
			storage[0] = int64(values)
			return storage[:1], true
		}
	case uint8:
		storage[0] = int64(values)
		return storage[:1], true
	case uint16:
		storage[0] = int64(values)
		return storage[:1], true
	case uint32:
		storage[0] = int64(values)
		return storage[:1], true
	case uint64:
		if values <= math.MaxInt64 {
			storage[0] = int64(values)
			return storage[:1], true
		}
	case []int:
		return numericSlice(values, storage), true
	case []int8:
		return numericSlice(values, storage), true
	case []int16:
		return numericSlice(values, storage), true
	case []int32:
		return numericSlice(values, storage), true
	case []int64:
		return values, true
	case []uint:
		return numericUnsignedSlice(values, storage)
	case []uint8:
		return numericSlice(values, storage), true
	case []uint16:
		return numericSlice(values, storage), true
	case []uint32:
		return numericSlice(values, storage), true
	case []uint64:
		return numericUnsignedSlice(values, storage)
	case []interface{}:
		if len(values) <= len(storage) {
			for idx, value := range values {
				var one [16]int64
				number, ok := numericQueryValues(value, &one)
				if !ok || len(number) != 1 {
					return nil, false
				}
				storage[idx] = number[0]
			}
			return storage[:len(values)], true
		}
		out := make([]int64, 0, len(values))
		for _, value := range values {
			var one [16]int64
			number, ok := numericQueryValues(value, &one)
			if !ok || len(number) != 1 {
				return nil, false
			}
			out = append(out, number[0])
		}
		return out, true
	}
	return nil, false
}

func numericSlice[T int | int8 | int16 | int32 | uint8 | uint16 | uint32](values []T, storage *[16]int64) []int64 {
	if len(values) <= len(storage) {
		for idx, value := range values {
			storage[idx] = int64(value)
		}
		return storage[:len(values)]
	}
	out := make([]int64, len(values))
	for idx, value := range values {
		out[idx] = int64(value)
	}
	return out
}

func numericUnsignedSlice[T uint | uint64](values []T, storage *[16]int64) ([]int64, bool) {
	if len(values) <= len(storage) {
		for idx, value := range values {
			if uint64(value) > math.MaxInt64 {
				return nil, false
			}
			storage[idx] = int64(value)
		}
		return storage[:len(values)], true
	}
	out := make([]int64, len(values))
	for idx, value := range values {
		if uint64(value) > math.MaxInt64 {
			return nil, false
		}
		out[idx] = int64(value)
	}
	return out, true
}

func prepareQuery(index *queryIndex, assignments be.Assignments) (preparedQuery, error) {
	fields := preparedQueryFieldPool.Get().([]queryFieldValue)[:0]
	if cap(fields) < len(assignments) {
		fields = make([]queryFieldValue, 0, len(assignments))
	}
	for field, raw := range assignments {
		var fieldIndex *queryFieldIndex
		if index != nil {
			fieldIndex = index.fields[string(field)]
			if fieldIndex == nil {
				continue
			}
		}
		fields = append(fields, queryFieldValue{field: field, index: fieldIndex})
		queryField := &fields[len(fields)-1]
		numbers, numeric := numericQueryValues(raw, &queryField.numberStorage)
		var values []string
		if !numeric {
			var err error
			values, err = parser.ValuesToStrings(raw)
			if err != nil {
				releasePreparedQuery(preparedQuery{fields: fields})
				return preparedQuery{}, err
			}
		}
		if (fieldIndex.includeRanges.hasEntries() || fieldIndex.excludeRanges.hasEntries()) && len(numbers) == 0 {
			numbers, _ = parser.ParseIntegers(raw, false)
		}
		queryField.values = values
		queryField.numbers = numbers
		queryField.numeric = numeric
	}
	return preparedQuery{fields: fields}, nil
}

func releasePreparedQuery(query preparedQuery) {
	fields := query.fields
	for idx := range fields {
		fields[idx] = queryFieldValue{}
	}
	if fields != nil {
		preparedQueryFieldPool.Put(fields[:0])
	}
}

func (f *queryFieldIndex) termContains(query queryFieldValue, exclude bool, group uint64) bool {
	if query.numeric {
		terms := f.includeNumbers
		if exclude {
			terms = f.excludeNumbers
		}
		for _, value := range query.numbers {
			if posting, ok := terms[value]; ok && posting.contains(group) {
				return true
			}
		}
		return false
	}
	terms := f.include
	if exclude {
		terms = f.exclude
	}
	for _, value := range query.values {
		if posting, ok := terms[value]; ok && posting.contains(group) {
			return true
		}
	}
	return false
}

func (f *queryFieldIndex) rangeContains(group uint64, numbers []int64, exclude bool) bool {
	if exclude {
		return f.excludeRanges.contains(group, numbers)
	}
	return f.includeRanges.contains(group, numbers)
}

// groupMatches evaluates one single-document group without constructing a
// bitmap. It is used by the all-single-group fast path; multi-document groups
// retain the slot bitmap path because one matching member must not match its
// siblings.
func (q *queryIndex) groupMatches(group uint64, query preparedQuery) bool {
	for _, queryField := range query.fields {
		field := queryField.index
		if field == nil {
			continue
		}
		if field.groupIncludeRequired.Contains(group) &&
			!field.termContains(queryField, false, group) &&
			!field.rangeContains(group, queryField.numbers, false) {
			return false
		}
		if field.termContains(queryField, true, group) ||
			field.rangeContains(group, queryField.numbers, true) {
			return false
		}
	}
	return true
}

// appendNumericPostingUnion is the int64-key counterpart of
// appendPostingUnion. Numeric query values use this path without formatting
// each value into a string.
func appendNumericPostingUnion(dst []uint64, terms map[int64]queryPosting, values []int64) []uint64 {
	if len(values) == 0 {
		return dst
	}
	if len(values) == 1 {
		if posting, ok := terms[values[0]]; ok {
			return posting.appendTo(dst)
		}
		return dst
	}
	start := len(dst)
	for _, value := range values {
		if posting, ok := terms[value]; ok {
			dst = posting.appendTo(dst)
		}
	}
	if len(dst) == start {
		return dst
	}
	sort.Slice(dst[start:], func(i, j int) bool { return dst[start+i] < dst[start+j] })
	out := dst[:start]
	for _, slot := range dst[start:] {
		if len(out) == 0 || out[len(out)-1] != slot {
			out = append(out, slot)
		}
	}
	return out
}

func (f *queryFieldIndex) appendTermUnion(dst []uint64, query queryFieldValue, exclude bool) []uint64 {
	if query.numeric {
		terms := f.includeNumbers
		if exclude {
			terms = f.excludeNumbers
		}
		return appendNumericPostingUnion(dst, terms, query.numbers)
	}
	terms := f.include
	if exclude {
		terms = f.exclude
	}
	return appendPostingUnion(dst, terms, query.values)
}

// appendPostingUnion appends the union of exact-term postings for values to dst.
// The returned slice is sorted and deduplicated when more than one posting is
// involved; single postings are copied to keep the caller's scratch independent.
func appendPostingUnion(dst []uint64, terms map[string]queryPosting, values []string) []uint64 {
	if len(values) == 0 {
		return dst
	}
	if len(values) == 1 {
		if posting, ok := terms[values[0]]; ok {
			return posting.appendTo(dst)
		}
		return dst
	}
	start := len(dst)
	for _, value := range values {
		if posting, ok := terms[value]; ok {
			dst = posting.appendTo(dst)
		}
	}
	if len(dst) == start {
		return dst
	}
	sort.Slice(dst[start:], func(i, j int) bool { return dst[start+i] < dst[start+j] })
	out := dst[:start]
	for _, slot := range dst[start:] {
		if len(out) == 0 || out[len(out)-1] != slot {
			out = append(out, slot)
		}
	}
	return out
}

// matchGroupIDs is the fast path for indexes whose groups all contain exactly
// one document and whose query has a selective required exact-term field. It
// chooses that posting as the candidate source, checks every query constraint,
// and maps surviving groups directly to document IDs without constructing a
// bitmap or an intermediate candidate slice. When no such field exists, callers
// use the bitmap path: scanning every group is slower for broad recall queries.
// normalizeDocIDs sorts and deduplicates a result while avoiding the
// reflection-based sort call for the common already-sorted posting case.
func normalizeDocIDs(result []be.DocID) []be.DocID {
	if len(result) < 2 {
		return result
	}
	sorted := true
	for idx := 1; idx < len(result); idx++ {
		if result[idx-1] > result[idx] {
			sorted = false
			break
		}
	}
	if !sorted {
		sort.Slice(result, func(a, b int) bool { return result[a] < result[b] })
	}
	unique := result[:0]
	for _, id := range result {
		if len(unique) == 0 || unique[len(unique)-1] != id {
			unique = append(unique, id)
		}
	}
	return unique
}

func (q *queryIndex) matchGroupIDs(query preparedQuery, tomb *tombstoneSet, metas []groupMeta) []be.DocID {
	result, ok := q.matchGroupIDsInto(query, tomb, metas, nil)
	if !ok {
		return nil
	}
	return result
}

// matchGroupIDsInto is the allocation-conscious form of matchGroupIDs. dst is
// treated as a reusable result buffer and reset before matches are appended;
// the bool reports whether the all-single fast path was applicable.
func (q *queryIndex) matchGroupIDsInto(query preparedQuery, tomb *tombstoneSet, metas []groupMeta, dst []be.DocID) ([]be.DocID, bool) {
	if q == nil || !q.allSingle {
		return dst, false
	}
	sourceField := -1
	sourceValues := 0
	for idx, queryField := range query.fields {
		field := queryField.index
		if field == nil || field.groupIncludeRequired.GetCardinality() != uint64(len(q.groupSlotStart)) {
			continue
		}
		if field.includeRanges.hasEntries() || field.groupIncludeRanges.hasEntries() {
			continue
		}
		count := 0
		if queryField.numeric {
			for _, value := range queryField.numbers {
				if posting, ok := field.groupIncludeNumbers[value]; ok {
					count += posting.count
				}
			}
		} else {
			for _, value := range queryField.values {
				if posting, ok := field.groupInclude[value]; ok {
					count += posting.count
				}
			}
		}
		if sourceField < 0 || count < sourceValues {
			sourceField = idx
			sourceValues = count
		}
	}
	if sourceField < 0 {
		return dst, false
	}
	result := dst[:0]
	queryField := query.fields[sourceField]
	field := queryField.index
	if queryField.numeric {
		for _, value := range queryField.numbers {
			if posting, ok := field.groupIncludeNumbers[value]; ok {
				result = q.appendPostingMatches(result, posting, query, tomb, metas)
			}
		}
	} else {
		for _, value := range queryField.values {
			if posting, ok := field.groupInclude[value]; ok {
				result = q.appendPostingMatches(result, posting, query, tomb, metas)
			}
		}
	}
	return normalizeDocIDs(result), true
}
func (q *queryIndex) appendPostingMatches(dst []be.DocID, posting queryPosting, query preparedQuery, tomb *tombstoneSet, metas []groupMeta) []be.DocID {
	switch posting.count {
	case 0:
	case 1:
		if q.groupMatches(posting.single, query) {
			dst = q.appendGroupDoc(dst, posting.single, tomb, metas)
		}
	default:
		for _, group := range posting.slots {
			if q.groupMatches(group, query) {
				dst = q.appendGroupDoc(dst, group, tomb, metas)
			}
		}
	}
	return dst
}

func (q *queryIndex) appendGroupDoc(dst []be.DocID, group uint64, tomb *tombstoneSet, metas []groupMeta) []be.DocID {
	if group >= uint64(len(metas)) || group >= uint64(len(q.slotDocs)) {
		return dst
	}
	id := q.slotDocs[group]
	if metas[group].dead(id, tomb) {
		return dst
	}
	return append(dst, be.DocID(id))
}

func (x *queryRangeIndex) hasEntries() bool {
	return x != nil && (len(x.entries) != 0 || len(x.points) != 0)
}

// matchGroups prunes conjunction groups with compact group-level postings.
// Single-document groups are complete after this step. Multi-document groups
// are retained for per-document evaluation so one matching member cannot
// incorrectly match its siblings.
func (q *queryIndex) matchGroups(query preparedQuery, scratch *queryScratch) *roaring64.Bitmap {
	if q == nil || scratch == nil {
		return nil
	}
	candidate := scratch.candidate
	candidate.Clear()
	candidate.Or(q.allGroups)
	includeHits := scratch.includeHits
	excludeHits := scratch.excludeHits
	missing := scratch.missing
	for _, queryField := range query.fields {
		field := queryField.index
		if field == nil {
			continue
		}
		includeHits.Clear()
		field.addGroupTermHits(includeHits, queryField, false)
		field.groupIncludeRanges.addHits(includeHits, queryField.numbers)
		if !field.groupIncludeRequired.IsEmpty() {
			// Keep only candidate groups that satisfy this required field,
			// while preserving groups that do not constrain the field.
			includeHits.And(candidate)
			includeHits.And(field.groupIncludeRequired)
			// For broad terms, subtracting the relatively small miss set is
			// cheaper. For selective terms, avoid cloning the large required
			// set and rebuild the candidate from the small hit set instead.
			if includeHits.GetCardinality()*10 >= field.groupIncludeRequired.GetCardinality() {
				missing.Clear()
				missing.Or(field.groupIncludeRequired)
				missing.AndNot(includeHits)
				candidate.AndNot(missing)
			} else {
				candidate.AndNot(field.groupIncludeRequired)
				candidate.Or(includeHits)
			}
		}
		excludeHits.Clear()
		field.addGroupTermHits(excludeHits, queryField, true)
		field.groupExcludeRanges.addHits(excludeHits, queryField.numbers)
		// Excluding one member is only a safe group-level prune when the group
		// has exactly one document. Multi-document groups are filtered below.
		excludeHits.And(q.single)
		candidate.AndNot(excludeHits)
		if candidate.IsEmpty() {
			break
		}
	}
	return candidate
}

// matchGroupSlots evaluates one multi-document group over its dense slot
// range. This is intentionally separate from matchGroups: group-level include
// hits can over-match a merged group, while excludes must remain doc-local.
func (q *queryIndex) matchGroupSlots(group uint64, query preparedQuery, scratch *queryScratch) *roaring64.Bitmap {
	if group >= uint64(len(q.groupSlotStart)) || scratch == nil {
		return nil
	}
	start, end := q.groupSlotStart[group], q.groupSlotEnd[group]
	result := scratch.slots
	result.Clear()
	result.AddRange(start, end)
	includeHits := scratch.includeHits
	excludeHits := scratch.excludeHits
	for _, queryField := range query.fields {
		field := queryField.index
		if field == nil {
			continue
		}
		includeHits.Clear()
		field.addTermHits(includeHits, queryField, false)
		field.includeRanges.addHits(includeHits, queryField.numbers)
		if !field.includeRequired.IsEmpty() {
			includeHits.And(result)
			includeHits.And(field.includeRequired)
			result.AndNot(field.includeRequired)
			result.Or(includeHits)
		}
		excludeHits.Clear()
		field.addTermHits(excludeHits, queryField, true)
		field.excludeRanges.addHits(excludeHits, queryField.numbers)
		result.AndNot(excludeHits)
		if result.IsEmpty() {
			break
		}
	}
	return result
}

// collectGroupDocs maps matching groups and their per-document slots back to
// document IDs while preserving tombstone sequence checks and DNF result
// deduplication.
func (i *Index) collectGroupDocs(groups *roaring64.Bitmap, query preparedQuery, tomb *tombstoneSet) ([]be.DocID, error) {
	return i.collectGroupDocsInto(groups, query, tomb, nil, nil)
}

// collectGroupDocsInto maps matching groups and their per-document slots back
// to document IDs while preserving tombstone sequence checks and DNF result
// deduplication. dst is a reusable result buffer and is reset before matching.
func (i *Index) collectGroupDocsInto(groups *roaring64.Bitmap, query preparedQuery, tomb *tombstoneSet, scratch *queryScratch, dst []be.DocID) ([]be.DocID, error) {
	if groups == nil {
		return dst[:0], nil
	}
	if scratch == nil {
		scratch = acquireQueryScratch()
		defer releaseQueryScratch(scratch)
	}
	result := dst[:0]
	iterator := groups.Iterator()
	for iterator.HasNext() {
		group := iterator.Next()
		if group >= uint64(len(i.groups)) {
			continue
		}
		meta := &i.groups[group]
		if len(meta.docs) == 1 {
			id := meta.docs[0]
			if !meta.dead(id, tomb) {
				result = append(result, be.DocID(id))
			}
			continue
		}
		slots := i.query.matchGroupSlots(group, query, scratch)
		if slots == nil {
			continue
		}
		slotIterator := slots.Iterator()
		for slotIterator.HasNext() {
			slot := slotIterator.Next()
			if slot >= uint64(len(i.query.slotDocs)) {
				continue
			}
			id := i.query.slotDocs[slot]
			if meta.dead(id, tomb) {
				continue
			}
			result = append(result, be.DocID(id))
		}
	}
	return normalizeDocIDs(result), nil
}
