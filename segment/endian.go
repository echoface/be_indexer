package segment

import (
	"encoding/binary"
	"unsafe"
)

var nativeLittleEndian = func() bool {
	var word uint16 = 1
	return *(*byte)(unsafe.Pointer(&word)) == 1
}()

// mapLittleEndian borrows aligned storage only on little-endian hosts. On big-
// endian hosts or unaligned input it decodes into an owned slice. Callers must
// validate count*width against len(raw) before calling this helper.
// The explicit endian argument also lets tests exercise the portable path on
// little-endian CI hosts without mutating process-wide state.
func mapLittleEndian[T ~uint32 | ~uint64](raw []byte, count uint32, littleEndian bool) []T {
	if count == 0 {
		return nil
	}
	var value T
	width := int(unsafe.Sizeof(value))
	ptr := unsafe.Pointer(&raw[0])
	if littleEndian && uintptr(ptr)%uintptr(width) == 0 {
		return unsafe.Slice((*T)(ptr), int(count))
	}
	values := make([]T, int(count))
	for i := range values {
		off := i * width
		if width == 8 {
			values[i] = T(binary.LittleEndian.Uint64(raw[off : off+8]))
		} else {
			values[i] = T(binary.LittleEndian.Uint32(raw[off : off+4]))
		}
	}
	return values
}
