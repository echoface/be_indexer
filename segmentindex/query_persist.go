package segmentindex

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/RoaringBitmap/roaring/roaring64"
)

const (
	queryCacheMagic   = "QIDX01"
	queryCacheVersion = uint16(1)
)

// appendQueryIndex serializes the query-facing side of an immutable Index.
// The cache keeps the dense slot arrays, term lookup maps, range trees, and
// bitmaps used by RetrieveAssignments, so loading a server-side index does not
// have to replay every segment posting to rebuild them.
func appendQueryIndex(dst []byte, query *queryIndex, groups []groupMeta) ([]byte, error) {
	if query == nil {
		return nil, fmt.Errorf("%w: nil query index", ErrInvalidIndex)
	}
	if err := validateQueryIndexShape(query, groups); err != nil {
		return nil, err
	}

	dst = append(dst, queryCacheMagic...)
	dst = appendUint16(dst, queryCacheVersion)

	var err error
	if dst, err = appendBitmap(dst, query.all); err != nil {
		return nil, err
	}
	if dst, err = appendBitmap(dst, query.allGroups); err != nil {
		return nil, err
	}
	if dst, err = appendBitmap(dst, query.single); err != nil {
		return nil, err
	}
	if query.allSingle {
		dst = append(dst, 1)
	} else {
		dst = append(dst, 0)
	}

	if dst, err = appendUint64Slice(dst, query.slotGroup); err != nil {
		return nil, err
	}
	if dst, err = appendUint64Slice(dst, query.slotDocs); err != nil {
		return nil, err
	}
	if dst, err = appendUint64Slice(dst, query.groupSlotStart); err != nil {
		return nil, err
	}
	if dst, err = appendUint64Slice(dst, query.groupSlotEnd); err != nil {
		return nil, err
	}

	fields := make([]string, 0, len(query.fields))
	for field := range query.fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if dst, err = appendCount(dst, len(fields)); err != nil {
		return nil, err
	}
	for _, field := range fields {
		if dst, err = appendString(dst, field); err != nil {
			return nil, err
		}
		if dst, err = appendQueryField(dst, query.fields[field]); err != nil {
			return nil, fmt.Errorf("field %q: %w", field, err)
		}
	}
	return dst, nil
}

// readQueryIndex restores a queryIndex from appendQueryIndex output and checks
// that its slot domain agrees with the already decoded group metadata.
func readQueryIndex(data []byte, groups []groupMeta) (*queryIndex, error) {
	if len(data) < len(queryCacheMagic)+2 {
		return nil, fmt.Errorf("%w: short query cache", ErrInvalidIndex)
	}
	if string(data[:len(queryCacheMagic)]) != queryCacheMagic {
		return nil, fmt.Errorf("%w: bad query cache magic", ErrInvalidIndex)
	}
	reader := bytes.NewReader(data[len(queryCacheMagic):])
	version, err := readUint16(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: query cache version: %v", ErrInvalidIndex, err)
	}
	if version != queryCacheVersion {
		return nil, fmt.Errorf("%w: unsupported query cache version %d", ErrInvalidIndex, version)
	}

	query := &queryIndex{}
	if query.all, err = readBitmap(reader); err != nil {
		return nil, fmt.Errorf("%w: query all bitmap: %v", ErrInvalidIndex, err)
	}
	if query.allGroups, err = readBitmap(reader); err != nil {
		return nil, fmt.Errorf("%w: query group bitmap: %v", ErrInvalidIndex, err)
	}
	if query.single, err = readBitmap(reader); err != nil {
		return nil, fmt.Errorf("%w: query single bitmap: %v", ErrInvalidIndex, err)
	}
	allSingle, err := readByte(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: query all-single flag: %v", ErrInvalidIndex, err)
	}
	if allSingle > 1 {
		return nil, fmt.Errorf("%w: invalid query all-single flag %d", ErrInvalidIndex, allSingle)
	}
	query.allSingle = allSingle == 1

	if query.slotGroup, err = readUint64Slice(reader); err != nil {
		return nil, fmt.Errorf("%w: query slot groups: %v", ErrInvalidIndex, err)
	}
	if query.slotDocs, err = readUint64Slice(reader); err != nil {
		return nil, fmt.Errorf("%w: query slot docs: %v", ErrInvalidIndex, err)
	}
	if query.groupSlotStart, err = readUint64Slice(reader); err != nil {
		return nil, fmt.Errorf("%w: query group slot starts: %v", ErrInvalidIndex, err)
	}
	if query.groupSlotEnd, err = readUint64Slice(reader); err != nil {
		return nil, fmt.Errorf("%w: query group slot ends: %v", ErrInvalidIndex, err)
	}

	fieldCount, err := readCount(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: query field count: %v", ErrInvalidIndex, err)
	}
	if err := requireItemBudget(reader, fieldCount, 4+4); err != nil {
		return nil, fmt.Errorf("%w: query fields: %v", ErrInvalidIndex, err)
	}
	query.fields = make(map[string]*queryFieldIndex, fieldCount)
	for idx := uint32(0); idx < fieldCount; idx++ {
		field, err := readString(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: query field name: %v", ErrInvalidIndex, err)
		}
		if _, exists := query.fields[field]; exists {
			return nil, fmt.Errorf("%w: duplicate query field %q", ErrInvalidIndex, field)
		}
		value, err := readQueryField(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: query field %q: %v", ErrInvalidIndex, field, err)
		}
		query.fields[field] = value
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("%w: %d trailing query cache bytes", ErrInvalidIndex, reader.Len())
	}
	if err := validateQueryIndexShape(query, groups); err != nil {
		return nil, err
	}
	return query, nil
}

func appendQueryField(dst []byte, field *queryFieldIndex) ([]byte, error) {
	if field == nil {
		return nil, fmt.Errorf("%w: nil field index", ErrInvalidIndex)
	}
	var err error
	for _, writer := range []func([]byte) ([]byte, error){
		func(dst []byte) ([]byte, error) { return appendStringPostingMap(dst, field.include) },
		func(dst []byte) ([]byte, error) { return appendIntPostingMap(dst, field.includeNumbers) },
		func(dst []byte) ([]byte, error) { return appendStringPostingMap(dst, field.exclude) },
		func(dst []byte) ([]byte, error) { return appendIntPostingMap(dst, field.excludeNumbers) },
	} {
		if dst, err = writer(dst); err != nil {
			return nil, err
		}
	}
	if dst, err = appendBitmap(dst, field.includeRequired); err != nil {
		return nil, err
	}
	if dst, err = appendQueryRangeIndex(dst, field.includeRanges); err != nil {
		return nil, err
	}
	if dst, err = appendQueryRangeIndex(dst, field.excludeRanges); err != nil {
		return nil, err
	}

	for _, writer := range []func([]byte) ([]byte, error){
		func(dst []byte) ([]byte, error) { return appendStringPostingMap(dst, field.groupInclude) },
		func(dst []byte) ([]byte, error) { return appendIntPostingMap(dst, field.groupIncludeNumbers) },
		func(dst []byte) ([]byte, error) { return appendStringPostingMap(dst, field.groupExclude) },
		func(dst []byte) ([]byte, error) { return appendIntPostingMap(dst, field.groupExcludeNumbers) },
	} {
		if dst, err = writer(dst); err != nil {
			return nil, err
		}
	}
	if dst, err = appendBitmap(dst, field.groupIncludeRequired); err != nil {
		return nil, err
	}
	if dst, err = appendQueryRangeIndex(dst, field.groupIncludeRanges); err != nil {
		return nil, err
	}
	if dst, err = appendQueryRangeIndex(dst, field.groupExcludeRanges); err != nil {
		return nil, err
	}
	return dst, nil
}

func readQueryField(reader *bytes.Reader) (*queryFieldIndex, error) {
	field := newQueryFieldIndex()
	var err error
	if field.include, err = readStringPostingMap(reader); err != nil {
		return nil, err
	}
	if field.includeNumbers, err = readIntPostingMap(reader); err != nil {
		return nil, err
	}
	if field.exclude, err = readStringPostingMap(reader); err != nil {
		return nil, err
	}
	if field.excludeNumbers, err = readIntPostingMap(reader); err != nil {
		return nil, err
	}
	if field.includeRequired, err = readBitmap(reader); err != nil {
		return nil, err
	}
	if err = readQueryRangeIndex(reader, &field.includeRanges); err != nil {
		return nil, err
	}
	if err = readQueryRangeIndex(reader, &field.excludeRanges); err != nil {
		return nil, err
	}

	if field.groupInclude, err = readStringPostingMap(reader); err != nil {
		return nil, err
	}
	if field.groupIncludeNumbers, err = readIntPostingMap(reader); err != nil {
		return nil, err
	}
	if field.groupExclude, err = readStringPostingMap(reader); err != nil {
		return nil, err
	}
	if field.groupExcludeNumbers, err = readIntPostingMap(reader); err != nil {
		return nil, err
	}
	if field.groupIncludeRequired, err = readBitmap(reader); err != nil {
		return nil, err
	}
	if err = readQueryRangeIndex(reader, &field.groupIncludeRanges); err != nil {
		return nil, err
	}
	if err = readQueryRangeIndex(reader, &field.groupExcludeRanges); err != nil {
		return nil, err
	}
	return field, nil
}

func appendQueryPosting(dst []byte, posting queryPosting) ([]byte, error) {
	if err := validateQueryPosting(posting); err != nil {
		return nil, err
	}
	dst = appendUint64(dst, posting.single)
	dst, err := appendCount(dst, len(posting.slots))
	if err != nil {
		return nil, err
	}
	for _, slot := range posting.slots {
		dst = appendUint64(dst, slot)
	}
	dst, err = appendCount(dst, posting.count)
	if err != nil {
		return nil, err
	}
	return dst, nil
}

func readQueryPosting(reader *bytes.Reader) (queryPosting, error) {
	var posting queryPosting
	single, err := readUint64(reader)
	if err != nil {
		return posting, err
	}
	slotCount, err := readCount(reader)
	if err != nil {
		return posting, err
	}
	if err := requireItemBudget(reader, slotCount, 8); err != nil {
		return posting, err
	}
	posting.single = single
	posting.slots = make([]uint64, 0, slotCount)
	for idx := uint32(0); idx < slotCount; idx++ {
		slot, err := readUint64(reader)
		if err != nil {
			return posting, err
		}
		posting.slots = append(posting.slots, slot)
	}
	count, err := readCount(reader)
	if err != nil {
		return posting, err
	}
	posting.count = int(count)
	if err := validateQueryPosting(posting); err != nil {
		return posting, err
	}
	return posting, nil
}

func validateQueryPosting(posting queryPosting) error {
	if posting.count < 0 || uint64(posting.count) > uint64(^uint32(0)) {
		return fmt.Errorf("%w: posting count %d overflows uint32", ErrInvalidIndex, posting.count)
	}
	switch posting.count {
	case 0:
		if len(posting.slots) != 0 {
			return fmt.Errorf("%w: empty posting has %d slots", ErrInvalidIndex, len(posting.slots))
		}
	case 1:
		if len(posting.slots) != 0 {
			return fmt.Errorf("%w: single posting has %d slots", ErrInvalidIndex, len(posting.slots))
		}
	default:
		if len(posting.slots) != posting.count {
			return fmt.Errorf("%w: posting count %d has %d slots", ErrInvalidIndex, posting.count, len(posting.slots))
		}
		if posting.single != posting.slots[0] {
			return fmt.Errorf("%w: posting single=%d first slot=%d", ErrInvalidIndex, posting.single, posting.slots[0])
		}
		for idx := 1; idx < len(posting.slots); idx++ {
			if posting.slots[idx-1] >= posting.slots[idx] {
				return fmt.Errorf("%w: posting slots are not strictly increasing", ErrInvalidIndex)
			}
		}
	}
	return nil
}

func appendStringPostingMap(dst []byte, values map[string]queryPosting) ([]byte, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var err error
	if dst, err = appendCount(dst, len(keys)); err != nil {
		return nil, err
	}
	for _, key := range keys {
		if dst, err = appendString(dst, key); err != nil {
			return nil, err
		}
		if dst, err = appendQueryPosting(dst, values[key]); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func readStringPostingMap(reader *bytes.Reader) (map[string]queryPosting, error) {
	count, err := readCount(reader)
	if err != nil {
		return nil, err
	}
	if err := requireItemBudget(reader, count, 4+8+4+4); err != nil {
		return nil, err
	}
	values := make(map[string]queryPosting, count)
	for idx := uint32(0); idx < count; idx++ {
		key, err := readString(reader)
		if err != nil {
			return nil, err
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("%w: duplicate string posting key %q", ErrInvalidIndex, key)
		}
		posting, err := readQueryPosting(reader)
		if err != nil {
			return nil, err
		}
		values[key] = posting
	}
	return values, nil
}

func appendIntPostingMap(dst []byte, values map[int64]queryPosting) ([]byte, error) {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var err error
	if dst, err = appendCount(dst, len(keys)); err != nil {
		return nil, err
	}
	for _, key := range keys {
		dst = appendInt64(dst, key)
		if dst, err = appendQueryPosting(dst, values[key]); err != nil {
			return nil, err
		}
	}
	return dst, nil
}

func readIntPostingMap(reader *bytes.Reader) (map[int64]queryPosting, error) {
	count, err := readCount(reader)
	if err != nil {
		return nil, err
	}
	if err := requireItemBudget(reader, count, 8+8+4+4); err != nil {
		return nil, err
	}
	values := make(map[int64]queryPosting, count)
	for idx := uint32(0); idx < count; idx++ {
		key, err := readInt64(reader)
		if err != nil {
			return nil, err
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("%w: duplicate numeric posting key %d", ErrInvalidIndex, key)
		}
		posting, err := readQueryPosting(reader)
		if err != nil {
			return nil, err
		}
		values[key] = posting
	}
	return values, nil
}

func appendQueryRangeIndex(dst []byte, index queryRangeIndex) ([]byte, error) {
	entries := append([]queryRangeEntry(nil), index.entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].left == entries[j].left {
			if entries[i].right == entries[j].right {
				if entries[i].slotStart == entries[j].slotStart {
					return entries[i].slotEnd < entries[j].slotEnd
				}
				return entries[i].slotStart < entries[j].slotStart
			}
			return entries[i].right < entries[j].right
		}
		return entries[i].left < entries[j].left
	})
	if !queryRangeEntriesEqual(entries, index.entries) {
		return nil, fmt.Errorf("%w: range entries are not canonical", ErrInvalidIndex)
	}
	if err := validateQueryRangeIndex(index); err != nil {
		return nil, err
	}

	var err error
	if dst, err = appendCount(dst, len(entries)); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		dst = appendInt64(dst, entry.left)
		dst = appendInt64(dst, entry.right)
		dst = appendUint64(dst, entry.slotStart)
		dst = appendUint64(dst, entry.slotEnd)
	}

	points := make([]int64, 0, len(index.points))
	for point := range index.points {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	if dst, err = appendCount(dst, len(points)); err != nil {
		return nil, err
	}
	for _, point := range points {
		dst = appendInt64(dst, point)
		if dst, err = appendBitmap(dst, index.points[point]); err != nil {
			return nil, err
		}
	}

	if dst, err = appendCount(dst, len(index.nodes)); err != nil {
		return nil, err
	}
	for _, node := range index.nodes {
		dst = appendInt64(dst, int64(node.start))
		dst = appendInt64(dst, int64(node.end))
		dst = appendInt64(dst, node.maxRight)
		dst = appendInt64(dst, int64(node.left))
		dst = appendInt64(dst, int64(node.right))
	}
	dst = appendInt64(dst, int64(index.root))
	return dst, nil
}

func readQueryRangeIndex(reader *bytes.Reader, index *queryRangeIndex) error {
	entryCount, err := readCount(reader)
	if err != nil {
		return err
	}
	if err := requireItemBudget(reader, entryCount, 8+8+8+8); err != nil {
		return err
	}
	index.entries = make([]queryRangeEntry, 0, entryCount)
	for idx := uint32(0); idx < entryCount; idx++ {
		left, err := readInt64(reader)
		if err != nil {
			return err
		}
		right, err := readInt64(reader)
		if err != nil {
			return err
		}
		slotStart, err := readUint64(reader)
		if err != nil {
			return err
		}
		slotEnd, err := readUint64(reader)
		if err != nil {
			return err
		}
		index.entries = append(index.entries, queryRangeEntry{left: left, right: right, slotStart: slotStart, slotEnd: slotEnd})
	}

	pointCount, err := readCount(reader)
	if err != nil {
		return err
	}
	if err := requireItemBudget(reader, pointCount, 8+8); err != nil {
		return err
	}
	index.points = make(map[int64]*roaring64.Bitmap, pointCount)
	for idx := uint32(0); idx < pointCount; idx++ {
		point, err := readInt64(reader)
		if err != nil {
			return err
		}
		if _, exists := index.points[point]; exists {
			return fmt.Errorf("%w: duplicate range point %d", ErrInvalidIndex, point)
		}
		bitmap, err := readBitmap(reader)
		if err != nil {
			return err
		}
		index.points[point] = bitmap
	}

	nodeCount, err := readCount(reader)
	if err != nil {
		return err
	}
	if err := requireItemBudget(reader, nodeCount, 8+8+8+8+8); err != nil {
		return err
	}
	index.nodes = make([]queryRangeNode, 0, nodeCount)
	for idx := uint32(0); idx < nodeCount; idx++ {
		start, err := readInt64(reader)
		if err != nil {
			return err
		}
		end, err := readInt64(reader)
		if err != nil {
			return err
		}
		maxRight, err := readInt64(reader)
		if err != nil {
			return err
		}
		left, err := readInt64(reader)
		if err != nil {
			return err
		}
		right, err := readInt64(reader)
		if err != nil {
			return err
		}
		startInt, err := queryIndexToInt(start, "range node start")
		if err != nil {
			return err
		}
		endInt, err := queryIndexToInt(end, "range node end")
		if err != nil {
			return err
		}
		leftInt, err := queryNodeIndexToInt(left, "range node left")
		if err != nil {
			return err
		}
		rightInt, err := queryNodeIndexToInt(right, "range node right")
		if err != nil {
			return err
		}
		index.nodes = append(index.nodes, queryRangeNode{start: startInt, end: endInt, maxRight: maxRight, left: leftInt, right: rightInt})
	}
	root, err := readInt64(reader)
	if err != nil {
		return err
	}
	index.root, err = queryNodeIndexToInt(root, "range root")
	if err != nil {
		return err
	}
	return validateQueryRangeIndex(*index)
}

func appendBitmap(dst []byte, bitmap *roaring64.Bitmap) ([]byte, error) {
	if bitmap == nil {
		return nil, fmt.Errorf("%w: nil bitmap", ErrInvalidIndex)
	}
	encoded, err := bitmap.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("%w: bitmap: %v", ErrInvalidIndex, err)
	}
	dst, err = appendCount(dst, len(encoded))
	if err != nil {
		return nil, err
	}
	return append(dst, encoded...), nil
}

func readBitmap(reader *bytes.Reader) (*roaring64.Bitmap, error) {
	length, err := readCount(reader)
	if err != nil {
		return nil, err
	}
	if uint64(length) > uint64(reader.Len()) {
		return nil, fmt.Errorf("%w: bitmap length %d exceeds payload", ErrInvalidIndex, length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, err
	}
	bitmapReader := bytes.NewReader(data)
	bitmap := roaring64.New()
	if _, err := bitmap.ReadFrom(bitmapReader); err != nil {
		return nil, fmt.Errorf("%w: bitmap: %v", ErrInvalidIndex, err)
	}
	if bitmapReader.Len() != 0 {
		return nil, fmt.Errorf("%w: bitmap has %d trailing bytes", ErrInvalidIndex, bitmapReader.Len())
	}
	return bitmap, nil
}

func appendUint64Slice(dst []byte, values []uint64) ([]byte, error) {
	dst, err := appendCount(dst, len(values))
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		dst = appendUint64(dst, value)
	}
	return dst, nil
}

func readUint64Slice(reader *bytes.Reader) ([]uint64, error) {
	count, err := readCount(reader)
	if err != nil {
		return nil, err
	}
	if err := requireItemBudget(reader, count, 8); err != nil {
		return nil, err
	}
	values := make([]uint64, 0, count)
	for idx := uint32(0); idx < count; idx++ {
		value, err := readUint64(reader)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func queryRangeEntriesEqual(a, b []queryRangeEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if a[idx] != b[idx] {
			return false
		}
	}
	return true
}

func queryIndexToInt(value int64, name string) (int, error) {
	if value < 0 || uint64(value) > uint64(^uint(0)>>1) {
		return 0, fmt.Errorf("%w: %s %d out of range", ErrInvalidIndex, name, value)
	}
	return int(value), nil
}

func queryNodeIndexToInt(value int64, name string) (int, error) {
	if value == -1 {
		return -1, nil
	}
	return queryIndexToInt(value, name)
}

func validateQueryRangeIndex(index queryRangeIndex) error {
	for _, entry := range index.entries {
		if entry.left >= entry.right || entry.slotStart >= entry.slotEnd {
			return fmt.Errorf("%w: invalid range entry [%d,%d) slots [%d,%d)", ErrInvalidIndex, entry.left, entry.right, entry.slotStart, entry.slotEnd)
		}
	}
	for idx := 1; idx < len(index.entries); idx++ {
		previous := index.entries[idx-1]
		current := index.entries[idx]
		if previous.left > current.left ||
			(previous.left == current.left && previous.right > current.right) ||
			(previous.left == current.left && previous.right == current.right && previous.slotStart > current.slotStart) ||
			(previous.left == current.left && previous.right == current.right && previous.slotStart == current.slotStart && previous.slotEnd >= current.slotEnd) {
			return fmt.Errorf("%w: range entries are not strictly sorted", ErrInvalidIndex)
		}
	}
	if len(index.entries) == 0 {
		if index.root != -1 || len(index.nodes) != 0 {
			return fmt.Errorf("%w: empty range index has tree nodes", ErrInvalidIndex)
		}
	} else {
		if index.root != 0 || len(index.nodes) != 2*len(index.entries)-1 {
			return fmt.Errorf("%w: invalid range tree size root=%d nodes=%d entries=%d", ErrInvalidIndex, index.root, len(index.nodes), len(index.entries))
		}
		if err := validateQueryRangeNode(index, 0, 0, len(index.entries)); err != nil {
			return err
		}
	}
	for point, bitmap := range index.points {
		if bitmap == nil {
			return fmt.Errorf("%w: nil range point bitmap %d", ErrInvalidIndex, point)
		}
	}
	return nil
}

func validateQueryRangeNode(index queryRangeIndex, nodeIndex, start, end int) error {
	if nodeIndex < 0 || nodeIndex >= len(index.nodes) {
		return fmt.Errorf("%w: range tree node %d out of bounds", ErrInvalidIndex, nodeIndex)
	}
	node := index.nodes[nodeIndex]
	if node.start != start || node.end != end || start >= end {
		return fmt.Errorf("%w: range tree node %d bounds [%d,%d), want [%d,%d)", ErrInvalidIndex, nodeIndex, node.start, node.end, start, end)
	}
	maxRight := int64(math.MinInt64)
	for idx := start; idx < end; idx++ {
		if index.entries[idx].right > maxRight {
			maxRight = index.entries[idx].right
		}
	}
	if node.maxRight != maxRight {
		return fmt.Errorf("%w: range tree node %d maxRight=%d want %d", ErrInvalidIndex, nodeIndex, node.maxRight, maxRight)
	}
	if end-start == 1 {
		if node.left != -1 || node.right != -1 {
			return fmt.Errorf("%w: range leaf %d has children", ErrInvalidIndex, nodeIndex)
		}
		return nil
	}
	mid := start + (end-start)/2
	if node.left <= nodeIndex || node.right <= nodeIndex || node.left >= node.right {
		return fmt.Errorf("%w: range tree node %d has invalid children [%d,%d]", ErrInvalidIndex, nodeIndex, node.left, node.right)
	}
	if err := validateQueryRangeNode(index, node.left, start, mid); err != nil {
		return err
	}
	return validateQueryRangeNode(index, node.right, mid, end)
}

func validateQueryIndexShape(query *queryIndex, groups []groupMeta) error {
	if query == nil || query.all == nil || query.allGroups == nil || query.single == nil {
		return fmt.Errorf("%w: incomplete query index", ErrInvalidIndex)
	}
	slotCount := 0
	for _, meta := range groups {
		slotCount += len(meta.docs)
	}
	if len(query.slotGroup) != slotCount || len(query.slotDocs) != slotCount {
		return fmt.Errorf("%w: query slot arrays have %d/%d entries, want %d", ErrInvalidIndex, len(query.slotGroup), len(query.slotDocs), slotCount)
	}
	if len(query.groupSlotStart) != len(groups) || len(query.groupSlotEnd) != len(groups) {
		return fmt.Errorf("%w: query group slot arrays have %d/%d entries, want %d", ErrInvalidIndex, len(query.groupSlotStart), len(query.groupSlotEnd), len(groups))
	}
	if query.all.GetCardinality() != uint64(slotCount) || query.allGroups.GetCardinality() != uint64(len(groups)) {
		return fmt.Errorf("%w: query bitmap cardinality mismatch", ErrInvalidIndex)
	}
	singleCount := 0
	slot := 0
	for groupID, meta := range groups {
		start := query.groupSlotStart[groupID]
		end := query.groupSlotEnd[groupID]
		if start != uint64(slot) || end != uint64(slot+len(meta.docs)) {
			return fmt.Errorf("%w: group %d slot range [%d,%d), want [%d,%d)", ErrInvalidIndex, groupID, start, end, slot, slot+len(meta.docs))
		}
		if !query.allGroups.Contains(uint64(groupID)) {
			return fmt.Errorf("%w: missing group %d in allGroups", ErrInvalidIndex, groupID)
		}
		if len(meta.docs) == 1 {
			singleCount++
			if !query.single.Contains(uint64(groupID)) {
				return fmt.Errorf("%w: single group %d missing from single bitmap", ErrInvalidIndex, groupID)
			}
		} else if query.single.Contains(uint64(groupID)) {
			return fmt.Errorf("%w: multi-doc group %d in single bitmap", ErrInvalidIndex, groupID)
		}
		for offset, doc := range meta.docs {
			querySlot := slot + offset
			if query.slotGroup[querySlot] != uint64(groupID) || query.slotDocs[querySlot] != doc {
				return fmt.Errorf("%w: slot %d does not match group %d doc %d", ErrInvalidIndex, querySlot, groupID, doc)
			}
			if !query.all.Contains(uint64(querySlot)) {
				return fmt.Errorf("%w: slot %d missing from all bitmap", ErrInvalidIndex, querySlot)
			}
		}
		slot += len(meta.docs)
	}
	if query.single.GetCardinality() != uint64(singleCount) {
		return fmt.Errorf("%w: query single bitmap cardinality mismatch", ErrInvalidIndex)
	}
	wantAllSingle := len(groups) > 0 && singleCount == len(groups)
	if query.allSingle != wantAllSingle {
		return fmt.Errorf("%w: query allSingle=%t want %t", ErrInvalidIndex, query.allSingle, wantAllSingle)
	}
	for fieldName, field := range query.fields {
		if field == nil {
			return fmt.Errorf("%w: nil field index %q", ErrInvalidIndex, fieldName)
		}
		if field.includeRequired == nil || field.groupIncludeRequired == nil {
			return fmt.Errorf("%w: field %q missing required bitmap", ErrInvalidIndex, fieldName)
		}
		for _, postingMap := range []map[string]queryPosting{field.include, field.exclude, field.groupInclude, field.groupExclude} {
			for key, posting := range postingMap {
				if err := validateQueryPosting(posting); err != nil {
					return fmt.Errorf("%w: field %q term %q: %v", ErrInvalidIndex, fieldName, key, err)
				}
			}
		}
		for _, postingMap := range []map[int64]queryPosting{field.includeNumbers, field.excludeNumbers, field.groupIncludeNumbers, field.groupExcludeNumbers} {
			for key, posting := range postingMap {
				if err := validateQueryPosting(posting); err != nil {
					return fmt.Errorf("%w: field %q numeric term %d: %v", ErrInvalidIndex, fieldName, key, err)
				}
			}
		}
		for name, rangeIndex := range map[string]queryRangeIndex{
			"include": field.includeRanges, "exclude": field.excludeRanges,
			"group include": field.groupIncludeRanges, "group exclude": field.groupExcludeRanges,
		} {
			if err := validateQueryRangeIndex(rangeIndex); err != nil {
				return fmt.Errorf("%w: field %q %s range: %v", ErrInvalidIndex, fieldName, name, err)
			}
		}
	}
	return nil
}
