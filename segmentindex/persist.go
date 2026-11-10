package segmentindex

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"

	"github.com/echoface/be_indexer/storage"
)

const (
	indexMagic       = "BESIDX01"
	indexVersion     = uint16(1)
	indexHeaderSize  = 8 + 2 + 2 + 4 + 8
	indexChecksumLen = 4

	indexShapeHasInclude = uint8(1 << 0)
	indexShapeHasExclude = uint8(1 << 1)
	indexRangeInclude    = uint8(1 << 0)
)

var ErrInvalidIndex = errors.New("segmentindex: invalid index binary")

// MarshalBinary serializes an immutable Index into a versioned binary container.
// The posting/term portion of every group is delegated to storage.Segment;
// group metadata needed to preserve DNF and range semantics is encoded here.
func (i *Index) MarshalBinary() ([]byte, error) {
	if i == nil {
		return nil, fmt.Errorf("%w: nil index", ErrInvalidIndex)
	}
	if len(i.segments) != len(i.groups) {
		return nil, fmt.Errorf("%w: segments=%d groups=%d", ErrInvalidIndex, len(i.segments), len(i.groups))
	}

	payload := make([]byte, 0, 1024)
	for groupID, segment := range i.segments {
		if segment == nil {
			return nil, fmt.Errorf("%w: nil segment %d", ErrInvalidIndex, groupID)
		}
		encodedSegment, err := segment.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("marshal segment %d: %w", groupID, err)
		}
		payload = appendUint64(payload, uint64(len(encodedSegment)))
		payload = append(payload, encodedSegment...)

		meta := i.groups[groupID]
		payload, err = appendGroupMeta(payload, meta)
		if err != nil {
			return nil, fmt.Errorf("marshal group %d: %w", groupID, err)
		}
	}

	result := make([]byte, 0, indexHeaderSize+len(payload)+indexChecksumLen)
	result = append(result, indexMagic...)
	result = appendUint16(result, indexVersion)
	result = appendUint16(result, 0) // reserved
	result = appendUint32(result, uint32(len(i.segments)))
	result = appendUint64(result, uint64(len(payload)))
	result = append(result, payload...)

	var checksum [indexChecksumLen]byte
	binary.LittleEndian.PutUint32(checksum[:], crc32.ChecksumIEEE(result))
	return append(result, checksum[:]...), nil
}

// LoadIndex validates and loads an immutable Index produced by MarshalBinary.
func LoadIndex(data []byte) (*Index, error) {
	if len(data) < indexHeaderSize+indexChecksumLen {
		return nil, fmt.Errorf("%w: short file", ErrInvalidIndex)
	}
	if string(data[:len(indexMagic)]) != indexMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalidIndex)
	}

	storedChecksum := binary.LittleEndian.Uint32(data[len(data)-indexChecksumLen:])
	actualChecksum := crc32.ChecksumIEEE(data[:len(data)-indexChecksumLen])
	if storedChecksum != actualChecksum {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrInvalidIndex)
	}

	header := bytes.NewReader(data[len(indexMagic):indexHeaderSize])
	var version, reserved uint16
	var groupCount uint32
	var payloadLen uint64
	for _, target := range []interface{}{&version, &reserved, &groupCount, &payloadLen} {
		if err := binary.Read(header, binary.LittleEndian, target); err != nil {
			return nil, fmt.Errorf("%w: header: %v", ErrInvalidIndex, err)
		}
	}
	if version != indexVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrInvalidIndex, version)
	}
	if reserved != 0 {
		return nil, fmt.Errorf("%w: non-zero reserved header", ErrInvalidIndex)
	}

	available := uint64(len(data) - indexHeaderSize - indexChecksumLen)
	if payloadLen != available {
		return nil, fmt.Errorf("%w: payload length=%d available=%d", ErrInvalidIndex, payloadLen, available)
	}

	reader := bytes.NewReader(data[indexHeaderSize : len(data)-indexChecksumLen])
	const minGroupBytes = 8 + 4 + 4 + 4 + 4
	if uint64(groupCount)*minGroupBytes > uint64(reader.Len()) {
		return nil, fmt.Errorf("%w: group count %d exceeds payload", ErrInvalidIndex, groupCount)
	}
	segments := make([]*storage.Segment, 0, groupCount)
	groups := make([]groupMeta, 0, groupCount)
	for groupID := uint32(0); groupID < groupCount; groupID++ {
		segmentLen, err := readUint64(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: group %d segment length: %v", ErrInvalidIndex, groupID, err)
		}
		if segmentLen > uint64(reader.Len()) {
			return nil, fmt.Errorf("%w: group %d segment length exceeds payload", ErrInvalidIndex, groupID)
		}
		segmentData := make([]byte, int(segmentLen))
		if _, err := io.ReadFull(reader, segmentData); err != nil {
			return nil, fmt.Errorf("%w: group %d segment: %v", ErrInvalidIndex, groupID, err)
		}
		segment, err := storage.LoadSegment(segmentData)
		if err != nil {
			return nil, fmt.Errorf("%w: group %d segment: %v", ErrInvalidIndex, groupID, err)
		}

		meta, err := readGroupMeta(reader)
		if err != nil {
			return nil, fmt.Errorf("%w: group %d metadata: %v", ErrInvalidIndex, groupID, err)
		}
		segments = append(segments, segment)
		groups = append(groups, meta)
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("%w: %d trailing payload bytes", ErrInvalidIndex, reader.Len())
	}

	return newIndex(segments, groups), nil
}

func appendGroupMeta(dst []byte, meta groupMeta) ([]byte, error) {
	fields := make([]string, 0, len(meta.shapes))
	for field := range meta.shapes {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	var err error
	if dst, err = appendCount(dst, len(fields)); err != nil {
		return nil, err
	}
	for _, field := range fields {
		if dst, err = appendString(dst, field); err != nil {
			return nil, err
		}
		shape := meta.shapes[field]
		flags := uint8(0)
		if shape.hasIncl {
			flags |= indexShapeHasInclude
		}
		if shape.hasExcl {
			flags |= indexShapeHasExclude
		}
		dst = append(dst, flags)
	}

	ranges := append([]rangePredicate(nil), meta.ranges...)
	sort.Slice(ranges, func(a, b int) bool {
		if ranges[a].field != ranges[b].field {
			return ranges[a].field < ranges[b].field
		}
		if ranges[a].left != ranges[b].left {
			return ranges[a].left < ranges[b].left
		}
		if ranges[a].right != ranges[b].right {
			return ranges[a].right < ranges[b].right
		}
		return !ranges[a].incl && ranges[b].incl
	})
	if dst, err = appendCount(dst, len(ranges)); err != nil {
		return nil, err
	}
	for _, predicate := range ranges {
		if predicate.left > predicate.right {
			return nil, fmt.Errorf("%w: invalid range [%d,%d)", ErrInvalidIndex, predicate.left, predicate.right)
		}
		if dst, err = appendString(dst, predicate.field); err != nil {
			return nil, err
		}
		dst = appendInt64(dst, predicate.left)
		dst = appendInt64(dst, predicate.right)
		if predicate.incl {
			dst = append(dst, indexRangeInclude)
		} else {
			dst = append(dst, 0)
		}
	}

	seqDocs := make([]uint64, 0, len(meta.seqs))
	for docID := range meta.seqs {
		seqDocs = append(seqDocs, docID)
	}
	sort.Slice(seqDocs, func(a, b int) bool { return seqDocs[a] < seqDocs[b] })
	if dst, err = appendCount(dst, len(seqDocs)); err != nil {
		return nil, err
	}
	for _, docID := range seqDocs {
		dst = appendUint64(dst, docID)
		dst = appendUint64(dst, meta.seqs[docID])
	}

	docs := append([]uint64(nil), meta.docs...)
	sort.Slice(docs, func(a, b int) bool { return docs[a] < docs[b] })
	for idx := 1; idx < len(docs); idx++ {
		if docs[idx] == docs[idx-1] {
			return nil, fmt.Errorf("%w: duplicate doc id %d", ErrInvalidIndex, docs[idx])
		}
	}
	if dst, err = appendCount(dst, len(docs)); err != nil {
		return nil, err
	}
	for _, docID := range docs {
		dst = appendUint64(dst, docID)
	}
	return dst, nil
}

func readGroupMeta(reader *bytes.Reader) (groupMeta, error) {
	var meta groupMeta

	shapeCount, err := readCount(reader)
	if err != nil {
		return meta, err
	}
	if err := requireItemBudget(reader, shapeCount, 4+1); err != nil {
		return meta, err
	}
	meta.shapes = make(map[string]fieldShape, shapeCount)
	for idx := uint32(0); idx < shapeCount; idx++ {
		field, err := readString(reader)
		if err != nil {
			return meta, err
		}
		flags, err := readByte(reader)
		if err != nil {
			return meta, err
		}
		if flags&^(indexShapeHasInclude|indexShapeHasExclude) != 0 {
			return meta, fmt.Errorf("%w: invalid shape flags %d", ErrInvalidIndex, flags)
		}
		if _, exists := meta.shapes[field]; exists {
			return meta, fmt.Errorf("%w: duplicate shape field %q", ErrInvalidIndex, field)
		}
		meta.shapes[field] = fieldShape{
			hasIncl: flags&indexShapeHasInclude != 0,
			hasExcl: flags&indexShapeHasExclude != 0,
		}
	}

	rangeCount, err := readCount(reader)
	if err != nil {
		return meta, err
	}
	if err := requireItemBudget(reader, rangeCount, 4+8+8+1); err != nil {
		return meta, err
	}
	meta.ranges = make([]rangePredicate, 0, rangeCount)
	for idx := uint32(0); idx < rangeCount; idx++ {
		field, err := readString(reader)
		if err != nil {
			return meta, err
		}
		left, err := readInt64(reader)
		if err != nil {
			return meta, err
		}
		right, err := readInt64(reader)
		if err != nil {
			return meta, err
		}
		flags, err := readByte(reader)
		if err != nil {
			return meta, err
		}
		if flags&^indexRangeInclude != 0 {
			return meta, fmt.Errorf("%w: invalid range flags %d", ErrInvalidIndex, flags)
		}
		if left > right {
			return meta, fmt.Errorf("%w: invalid range [%d,%d)", ErrInvalidIndex, left, right)
		}
		meta.ranges = append(meta.ranges, rangePredicate{
			field: field,
			left:  left,
			right: right,
			incl:  flags&indexRangeInclude != 0,
		})
	}

	seqCount, err := readCount(reader)
	if err != nil {
		return meta, err
	}
	if err := requireItemBudget(reader, seqCount, 8+8); err != nil {
		return meta, err
	}
	meta.seqs = make(map[uint64]uint64, seqCount)
	for idx := uint32(0); idx < seqCount; idx++ {
		docID, err := readUint64(reader)
		if err != nil {
			return meta, err
		}
		seq, err := readUint64(reader)
		if err != nil {
			return meta, err
		}
		if _, exists := meta.seqs[docID]; exists {
			return meta, fmt.Errorf("%w: duplicate sequence doc id %d", ErrInvalidIndex, docID)
		}
		meta.seqs[docID] = seq
	}

	docCount, err := readCount(reader)
	if err != nil {
		return meta, err
	}
	if err := requireItemBudget(reader, docCount, 8); err != nil {
		return meta, err
	}
	meta.docs = make([]uint64, 0, docCount)
	for idx := uint32(0); idx < docCount; idx++ {
		docID, err := readUint64(reader)
		if err != nil {
			return meta, err
		}
		if len(meta.docs) > 0 && docID <= meta.docs[len(meta.docs)-1] {
			return meta, fmt.Errorf("%w: docs are not strictly increasing", ErrInvalidIndex)
		}
		meta.docs = append(meta.docs, docID)
	}
	return meta, nil
}

func appendCount(dst []byte, count int) ([]byte, error) {
	if count < 0 || uint64(count) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("%w: count %d overflows uint32", ErrInvalidIndex, count)
	}
	return appendUint32(dst, uint32(count)), nil
}

func appendString(dst []byte, value string) ([]byte, error) {
	if uint64(len(value)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("%w: string length %d overflows uint32", ErrInvalidIndex, len(value))
	}
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...), nil
}

func readCount(reader *bytes.Reader) (uint32, error) {
	return readUint32(reader)
}

// requireItemBudget rejects count fields that cannot fit in the remaining
// payload before allocating their maps or slices.
func requireItemBudget(reader *bytes.Reader, count uint32, minimumItemBytes uint32) error {
	needed := uint64(count) * uint64(minimumItemBytes)
	if needed > uint64(reader.Len()) {
		return fmt.Errorf("%w: item count %d exceeds payload", ErrInvalidIndex, count)
	}
	return nil
}

func readString(reader *bytes.Reader) (string, error) {
	length, err := readUint32(reader)
	if err != nil {
		return "", err
	}
	if uint64(length) > uint64(reader.Len()) {
		return "", fmt.Errorf("%w: string length %d exceeds payload", ErrInvalidIndex, length)
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func readByte(reader *bytes.Reader) (byte, error) {
	value, err := reader.ReadByte()
	if err != nil {
		return 0, err
	}
	return value, nil
}

func readUint16(reader *bytes.Reader) (uint16, error) {
	var value uint16
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func readUint32(reader *bytes.Reader) (uint32, error) {
	var value uint32
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func readUint64(reader *bytes.Reader) (uint64, error) {
	var value uint64
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func readInt64(reader *bytes.Reader) (int64, error) {
	var value int64
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func appendUint16(dst []byte, value uint16) []byte {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendInt64(dst []byte, value int64) []byte {
	return appendUint64(dst, uint64(value))
}
