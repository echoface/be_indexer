package segment

import (
	"fmt"
	"math"
)

// Segment format numeric limits. The on-disk format encodes several counts and
// block-relative offsets as fixed-width little-endian integers. Writing a value
// that exceeds its field width would silently truncate and produce a segment
// that loads but points at the wrong bytes. Build paths validate against these
// ceilings and return an error instead, so an oversized field fails the build
// loudly rather than corrupting the index.
const (
	// MaxBlockSize bounds a single block's payload. Block-relative offsets are
	// stored as uint32 (e.g. FlatDict term offsets, the range posting region),
	// so no single block may exceed 4 GiB - 1.
	MaxBlockSize = math.MaxUint32

	// MaxPostingCount bounds the number of EntryIDs in one posting list; the
	// count header is a uint32.
	MaxPostingCount = math.MaxUint32

	// MaxACOutputPerState bounds how many pattern outputs a single AC automaton
	// state may carry; the per-state count is a uint16.
	MaxACOutputPerState = math.MaxUint16

	// MaxDenseFieldID bounds the number of distinct fields in one segment; each
	// field is assigned a dense uint16 id used as the integer block key.
	MaxDenseFieldID = math.MaxUint16
)

// checkedU32 converts a non-negative int to uint32, returning an error tagged
// with what overflowed instead of silently truncating.
func checkedU32(n int, what string) (uint32, error) {
	if n < 0 || n > MaxBlockSize {
		return 0, fmt.Errorf("%s: %d exceeds uint32 format limit %d", what, n, MaxBlockSize)
	}
	return uint32(n), nil
}

// checkedU16 converts a non-negative int to uint16, returning an error tagged
// with what overflowed instead of silently truncating.
func checkedU16(n int, what string) (uint16, error) {
	if n < 0 || n > MaxACOutputPerState {
		return 0, fmt.Errorf("%s: %d exceeds uint16 format limit %d", what, n, MaxACOutputPerState)
	}
	return uint16(n), nil
}
