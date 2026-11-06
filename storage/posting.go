package storage

import (
	"encoding/binary"
	"fmt"
)

// PostingEncoding identifies the physical representation of a sorted posting list.
type PostingEncoding uint8

const (
	PostingRaw PostingEncoding = iota
	PostingDeltaVarint
	PostingRunVarint
)

func (e PostingEncoding) String() string {
	switch e {
	case PostingRaw:
		return "raw"
	case PostingDeltaVarint:
		return "delta-varint"
	case PostingRunVarint:
		return "run-varint"
	default:
		return "unknown"
	}
}

func encodePosting(ids []uint64) (PostingEncoding, []byte, error) {
	if !isSortedUnique(ids) {
		return 0, nil, fmt.Errorf("posting list must be sorted and unique")
	}

	raw := make([]byte, len(ids)*8)
	for i, id := range ids {
		binary.LittleEndian.PutUint64(raw[i*8:], id)
	}

	delta := make([]byte, 0, len(ids)*2)
	previous := uint64(0)
	for _, id := range ids {
		var buf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(buf[:], id-previous)
		delta = append(delta, buf[:n]...)
		previous = id
	}

	runs := make([]byte, 0, len(ids)*2)
	for i := 0; i < len(ids); {
		start := ids[i]
		length := uint64(1)
		for i+int(length) < len(ids) && ids[i+int(length)] == start+length {
			length++
		}
		var buf [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(buf[:], start)
		runs = append(runs, buf[:n]...)
		n = binary.PutUvarint(buf[:], length)
		runs = append(runs, buf[:n]...)
		i += int(length)
	}

	encoding := PostingRaw
	data := raw
	if len(delta) < len(data) {
		encoding, data = PostingDeltaVarint, delta
	}
	if len(runs) < len(data) {
		encoding, data = PostingRunVarint, runs
	}
	return encoding, data, nil
}

func decodePosting(encoding PostingEncoding, data []byte, count uint32) ([]uint64, error) {
	result := make([]uint64, 0, count)
	switch encoding {
	case PostingRaw:
		if len(data)%8 != 0 || uint32(len(data)/8) != count {
			return nil, fmt.Errorf("invalid raw posting length: bytes=%d count=%d", len(data), count)
		}
		for offset := 0; offset < len(data); offset += 8 {
			result = append(result, binary.LittleEndian.Uint64(data[offset:]))
		}
	case PostingDeltaVarint:
		offset := 0
		previous := uint64(0)
		for uint32(len(result)) < count {
			delta, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return nil, fmt.Errorf("invalid delta posting at offset %d", offset)
			}
			previous += delta
			result = append(result, previous)
			offset += n
		}
		if offset != len(data) {
			return nil, fmt.Errorf("trailing delta posting bytes: %d", len(data)-offset)
		}
	case PostingRunVarint:
		offset := 0
		for uint32(len(result)) < count {
			start, n := binary.Uvarint(data[offset:])
			if n <= 0 {
				return nil, fmt.Errorf("invalid run start at offset %d", offset)
			}
			offset += n
			length, n := binary.Uvarint(data[offset:])
			if n <= 0 || length == 0 {
				return nil, fmt.Errorf("invalid run length at offset %d", offset)
			}
			offset += n
			for i := uint64(0); i < length; i++ {
				result = append(result, start+i)
				if uint32(len(result)) > count {
					return nil, fmt.Errorf("run posting exceeds declared count")
				}
			}
		}
		if offset != len(data) {
			return nil, fmt.Errorf("trailing run posting bytes: %d", len(data)-offset)
		}
	default:
		return nil, fmt.Errorf("unknown posting encoding %d", encoding)
	}
	if !isSortedUnique(result) {
		return nil, fmt.Errorf("decoded posting is not sorted and unique")
	}
	return result, nil
}

func isSortedUnique(ids []uint64) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			return false
		}
	}
	return true
}

func isSortedNonDecreasing(ids []uint64) bool {
	for i := 1; i < len(ids); i++ {
		if ids[i] < ids[i-1] {
			return false
		}
	}
	return true
}
