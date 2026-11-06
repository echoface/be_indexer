package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
)

const (
	segmentMagic   = "BESEG001"
	segmentVersion = uint16(2)
	segmentHeader  = 40
	termRecordSize = 40
)

var (
	ErrInvalidSegment = errors.New("invalid segment")
	ErrTermNotFound   = errors.New("term not found")
)

// TermID is the compact identifier of a field/value pair inside a Segment.
type TermID uint32

// TermKey identifies a normalized term in a field dictionary.
type TermKey struct {
	Field string
	Value string
}

type termRecord struct {
	termID   TermID
	fieldAt  uint32
	fieldLen uint32
	valueAt  uint32
	valueLen uint32
	postAt   uint64
	postLen  uint32
	count    uint32
	codec    PostingEncoding
}

// Segment is an immutable in-memory view backed by one contiguous byte region.
type Segment struct {
	version uint16
	fields  []string
	terms   []termRecord
	strings []byte
	data    []byte
	live    []uint64
	byKey   map[TermKey]TermID
}

// SegmentBuilder collects mutable data and seals it into an immutable Segment.
type SegmentBuilder struct {
	postings map[TermKey][]uint64
	liveDocs map[uint64]struct{}
}

func NewSegmentBuilder() *SegmentBuilder {
	return &SegmentBuilder{postings: make(map[TermKey][]uint64), liveDocs: make(map[uint64]struct{})}
}

// AddLiveDoc records a document in the Segment universe, including wildcard-only documents.
func (b *SegmentBuilder) AddLiveDoc(id uint64) error {
	if b == nil {
		return ErrInvalidSegment
	}
	b.liveDocs[id] = struct{}{}
	return nil
}

func (b *SegmentBuilder) AddPosting(field, value string, ids []uint64) error {
	if b == nil {
		return ErrInvalidSegment
	}
	if field == "" {
		return fmt.Errorf("field is empty")
	}
	if !isSortedNonDecreasing(ids) {
		return fmt.Errorf("term %s=%s: posting list must be sorted", field, value)
	}
	key := TermKey{Field: field, Value: value}
	b.postings[key] = append(b.postings[key], ids...)
	for _, id := range ids {
		b.liveDocs[id] = struct{}{}
	}
	return nil
}

// Seal validates, deduplicates and creates an immutable Segment.
func (b *SegmentBuilder) Seal() (*Segment, error) {
	if b == nil {
		return nil, ErrInvalidSegment
	}
	keys := make([]TermKey, 0, len(b.postings))
	for key := range b.postings {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Field == keys[j].Field {
			return keys[i].Value < keys[j].Value
		}
		return keys[i].Field < keys[j].Field
	})

	fields := make([]string, 0)
	fieldSeen := make(map[string]struct{})
	stringRegion := make([]byte, 0)
	fieldOffsets := make(map[string][2]uint32)
	for _, key := range keys {
		if _, ok := fieldSeen[key.Field]; !ok {
			fieldSeen[key.Field] = struct{}{}
			start := uint32(len(stringRegion))
			stringRegion = append(stringRegion, key.Field...)
			fieldOffsets[key.Field] = [2]uint32{start, uint32(len(key.Field))}
			fields = append(fields, key.Field)
		}
	}

	terms := make([]termRecord, 0, len(keys))
	data := make([]byte, 0)
	byKey := make(map[TermKey]TermID, len(keys))
	for id, key := range keys {
		ids := append([]uint64(nil), b.postings[key]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		ids = uniqueUint64(ids)
		codec, encoded, err := encodePosting(ids)
		if err != nil {
			return nil, err
		}
		valueAt := uint32(len(stringRegion))
		stringRegion = append(stringRegion, key.Value...)
		fieldAtLen := fieldOffsets[key.Field]
		record := termRecord{
			termID:   TermID(id),
			fieldAt:  fieldAtLen[0],
			fieldLen: fieldAtLen[1],
			valueAt:  valueAt,
			valueLen: uint32(len(key.Value)),
			postAt:   uint64(len(data)),
			postLen:  uint32(len(encoded)),
			count:    uint32(len(ids)),
			codec:    codec,
		}
		terms = append(terms, record)
		data = append(data, encoded...)
		byKey[key] = TermID(id)
	}
	live := make([]uint64, 0, len(b.liveDocs))
	for id := range b.liveDocs {
		live = append(live, id)
	}
	sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
	return &Segment{version: segmentVersion, fields: fields, terms: terms, strings: stringRegion, data: data, live: live, byKey: byKey}, nil
}

func uniqueUint64(ids []uint64) []uint64 {
	if len(ids) < 2 {
		return ids
	}
	out := ids[:1]
	for _, id := range ids[1:] {
		if id != out[len(out)-1] {
			out = append(out, id)
		}
	}
	return out
}

// LookupTerm returns the compact TermID for a field/value pair.
func (s *Segment) LookupTerm(field, value string) (TermID, bool) {
	if s == nil {
		return 0, false
	}
	id, ok := s.byKey[TermKey{Field: field, Value: value}]
	return id, ok
}

// TermKeys returns the term dictionary keys ordered by TermID, so key i
// belongs to TermID(i). The result is a copy safe for the caller to keep.
func (s *Segment) TermKeys() []TermKey {
	if s == nil {
		return nil
	}
	keys := make([]TermKey, 0, len(s.terms))
	for _, record := range s.terms {
		keys = append(keys, TermKey{
			Field: string(s.strings[record.fieldAt : record.fieldAt+record.fieldLen]),
			Value: string(s.strings[record.valueAt : record.valueAt+record.valueLen]),
		})
	}
	return keys
}

// Posting returns a decoded copy of the posting list for a TermID.
func (s *Segment) Posting(id TermID) ([]uint64, error) {
	if s == nil || int(id) >= len(s.terms) {
		return nil, ErrTermNotFound
	}
	record := s.terms[id]
	end := record.postAt + uint64(record.postLen)
	if end > uint64(len(s.data)) {
		return nil, ErrInvalidSegment
	}
	return decodePosting(record.codec, s.data[record.postAt:end], record.count)
}

// PostingFor returns a decoded posting list by field and value.
// HasTermPrefix reports whether a field has at least one term with a value prefix.
func (s *Segment) HasTermPrefix(field, prefix string) bool {
	if s == nil {
		return false
	}
	for _, record := range s.terms {
		if uint64(record.fieldAt)+uint64(record.fieldLen) > uint64(len(s.strings)) || uint64(record.valueAt)+uint64(record.valueLen) > uint64(len(s.strings)) {
			continue
		}
		if string(s.strings[record.fieldAt:record.fieldAt+record.fieldLen]) != field {
			continue
		}
		value := string(s.strings[record.valueAt : record.valueAt+record.valueLen])
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (s *Segment) PostingFor(field, value string) ([]uint64, error) {
	id, ok := s.LookupTerm(field, value)
	if !ok {
		return nil, ErrTermNotFound
	}
	return s.Posting(id)
}

// Fields returns a copy of the sorted field dictionary.
func (s *Segment) Fields() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.fields...)
}

// TermCount returns the number of field/value terms in the Segment.
func (s *Segment) TermCount() int {
	if s == nil {
		return 0
	}
	return len(s.terms)
}

// AllDocIDs returns the explicit Segment live-document universe.
func (s *Segment) AllDocIDs() ([]uint64, error) {
	if s == nil {
		return nil, ErrInvalidSegment
	}
	return append([]uint64(nil), s.live...), nil
}

// MarshalBinary serializes the immutable segment with a CRC32 checksum.
func (s *Segment) MarshalBinary() ([]byte, error) {
	if s == nil || s.version != segmentVersion {
		return nil, ErrInvalidSegment
	}
	metadata := bytes.NewBuffer(make([]byte, 0, segmentHeader+len(s.terms)*termRecordSize))
	metadata.WriteString(segmentMagic)
	binary.Write(metadata, binary.LittleEndian, s.version)
	binary.Write(metadata, binary.LittleEndian, uint16(0))
	binary.Write(metadata, binary.LittleEndian, uint32(len(s.fields)))
	binary.Write(metadata, binary.LittleEndian, uint32(len(s.terms)))
	binary.Write(metadata, binary.LittleEndian, uint32(len(s.strings)))
	binary.Write(metadata, binary.LittleEndian, uint64(len(s.data)))
	binary.Write(metadata, binary.LittleEndian, uint64(len(s.live)))
	for _, record := range s.terms {
		binary.Write(metadata, binary.LittleEndian, record.termID)
		binary.Write(metadata, binary.LittleEndian, record.fieldAt)
		binary.Write(metadata, binary.LittleEndian, record.fieldLen)
		binary.Write(metadata, binary.LittleEndian, record.valueAt)
		binary.Write(metadata, binary.LittleEndian, record.valueLen)
		binary.Write(metadata, binary.LittleEndian, record.postAt)
		binary.Write(metadata, binary.LittleEndian, record.postLen)
		binary.Write(metadata, binary.LittleEndian, record.count)
		metadata.WriteByte(byte(record.codec))
		metadata.Write([]byte{0, 0, 0})
	}
	for _, field := range s.fields {
		binary.Write(metadata, binary.LittleEndian, uint32(len(field)))
		metadata.WriteString(field)
	}
	metadata.Write(s.strings)
	metadata.Write(s.data)
	for _, id := range s.live {
		binary.Write(metadata, binary.LittleEndian, id)
	}
	checksum := crc32.ChecksumIEEE(metadata.Bytes())
	binary.Write(metadata, binary.LittleEndian, checksum)
	return metadata.Bytes(), nil
}

// LoadSegment validates and loads a Segment from its binary representation.
func LoadSegment(data []byte) (*Segment, error) {
	if len(data) < segmentHeader+4 || string(data[:len(segmentMagic)]) != segmentMagic {
		return nil, ErrInvalidSegment
	}
	storedChecksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != storedChecksum {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrInvalidSegment)
	}
	reader := bytes.NewReader(data)
	magic := make([]byte, len(segmentMagic))
	_, _ = io.ReadFull(reader, magic)
	var version, reserved uint16
	var fieldCount, termCount, stringLen uint32
	var dataLen, liveCount uint64
	for _, target := range []interface{}{&version, &reserved, &fieldCount, &termCount, &stringLen, &dataLen, &liveCount} {
		if err := binary.Read(reader, binary.LittleEndian, target); err != nil {
			return nil, fmt.Errorf("%w: header: %v", ErrInvalidSegment, err)
		}
	}
	if string(magic) != segmentMagic || version != segmentVersion {
		return nil, ErrInvalidSegment
	}
	terms := make([]termRecord, termCount)
	for i := range terms {
		if err := binary.Read(reader, binary.LittleEndian, &terms[i].termID); err != nil {
			return nil, ErrInvalidSegment
		}
		for _, target := range []interface{}{&terms[i].fieldAt, &terms[i].fieldLen, &terms[i].valueAt, &terms[i].valueLen, &terms[i].postAt, &terms[i].postLen, &terms[i].count} {
			if err := binary.Read(reader, binary.LittleEndian, target); err != nil {
				return nil, ErrInvalidSegment
			}
		}
		codec, err := reader.ReadByte()
		if err != nil {
			return nil, ErrInvalidSegment
		}
		terms[i].codec = PostingEncoding(codec)
		padding := make([]byte, 3)
		if _, err := io.ReadFull(reader, padding); err != nil {
			return nil, ErrInvalidSegment
		}
	}
	fields := make([]string, fieldCount)
	for i := range fields {
		var length uint32
		if err := binary.Read(reader, binary.LittleEndian, &length); err != nil || uint64(length) > uint64(reader.Len()) {
			return nil, ErrInvalidSegment
		}
		value := make([]byte, length)
		if _, err := io.ReadFull(reader, value); err != nil {
			return nil, ErrInvalidSegment
		}
		fields[i] = string(value)
	}
	stringsData := make([]byte, stringLen)
	if _, err := io.ReadFull(reader, stringsData); err != nil {
		return nil, ErrInvalidSegment
	}
	postingData := make([]byte, dataLen)
	if _, err := io.ReadFull(reader, postingData); err != nil {
		return nil, ErrInvalidSegment
	}
	if liveCount > uint64(reader.Len()-4)/8 {
		return nil, ErrInvalidSegment
	}
	live := make([]uint64, liveCount)
	for i := range live {
		if err := binary.Read(reader, binary.LittleEndian, &live[i]); err != nil {
			return nil, ErrInvalidSegment
		}
	}
	if reader.Len() != 4 {
		return nil, ErrInvalidSegment
	}
	byKey := make(map[TermKey]TermID, len(terms))
	segment := &Segment{version: version, fields: fields, terms: terms, strings: stringsData, data: postingData, live: live, byKey: byKey}
	for _, record := range terms {
		if uint64(record.fieldAt)+uint64(record.fieldLen) > uint64(len(stringsData)) || uint64(record.valueAt)+uint64(record.valueLen) > uint64(len(stringsData)) {
			return nil, ErrInvalidSegment
		}
		field := string(stringsData[record.fieldAt : record.fieldAt+record.fieldLen])
		value := string(stringsData[record.valueAt : record.valueAt+record.valueLen])
		byKey[TermKey{Field: field, Value: value}] = record.termID
		if _, err := segment.Posting(record.termID); err != nil {
			return nil, err
		}
	}
	return segment, nil
}
