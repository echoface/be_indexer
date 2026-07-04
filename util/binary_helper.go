package util

import "encoding/binary"

func AppendVarint(buf []byte, x uint64) []byte {
	var temp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(temp[:], x)
	return append(buf, temp[:n]...)
}
