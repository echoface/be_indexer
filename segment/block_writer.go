package segment

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
)

// segmentBlockWriter implements BlockWriter for a single field within a segment.
// It derives full block names from the field name and kind, handles 8-byte
// alignment, offset tracking, checksums, and block registration.
type segmentBlockWriter struct {
	field       string
	w           io.Writer
	off         *uint64
	blockIndex  map[string]BlockDef
	checksums   map[string]string
	curChecksum hash.Hash
}

func newSegmentBlockWriter(
	field string,
	w io.Writer,
	off *uint64,
	blockIndex map[string]BlockDef,
	checksums map[string]string,
) *segmentBlockWriter {
	return &segmentBlockWriter{
		field:      field,
		w:          w,
		off:        off,
		blockIndex: blockIndex,
		checksums:  checksums,
	}
}

func (bw *segmentBlockWriter) WriteBlock(kind string, data []byte) error {
	name := bw.field + "_" + kind
	if err := bw.alignTo8(); err != nil {
		return err
	}
	offset := *bw.off
	bw.beginChecksum()
	if err := bw.writeBytes(data); err != nil {
		return err
	}
	sum := bw.finishChecksum()
	size := *bw.off - offset
	bw.blockIndex[name] = BlockDef{
		Field: bw.field,
		Kind:  kind,
		Offset: offset,
		Size:   size,
	}
	if sum != "" {
		bw.checksums[name] = sum
	}
	return nil
}

func (bw *segmentBlockWriter) alignTo8() error {
	pad := (8 - int(*bw.off%8)) % 8
	if pad == 0 {
		return nil
	}
	return bw.writeBytes(make([]byte, pad))
}

func (bw *segmentBlockWriter) beginChecksum() {
	bw.curChecksum = sha256.New()
}

func (bw *segmentBlockWriter) finishChecksum() string {
	if bw.curChecksum == nil {
		return ""
	}
	sum := checksumPrefix + hex.EncodeToString(bw.curChecksum.Sum(nil))
	bw.curChecksum = nil
	return sum
}

func (bw *segmentBlockWriter) writeBytes(b []byte) error {
	n, err := bw.w.Write(b)
	*bw.off += uint64(n)
	if bw.curChecksum != nil && n > 0 {
		bw.curChecksum.Write(b[:n])
	}
	if err != nil {
		return err
	}
	if n != len(b) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(b))
	}
	return nil
}

/*
// WriteWildcardsBlock writes the wildcard entries block.
func (sw *segmentBlockWriter) WriteWildcardsBlock(entries core.Entries) error {
	...
}
*/
