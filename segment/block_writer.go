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
	s, err := bw.OpenBlock(kind)
	if err != nil {
		return err
	}
	if _, err := s.Write(data); err != nil {
		return err
	}
	return s.Close()
}

// OpenBlock begins a streamed block. It aligns the stream to 8 bytes, records
// the post-alignment offset, and starts an incremental checksum. The returned
// blockStream feeds bytes straight through writeBytes (advancing the shared
// offset and checksum) so a large payload never has to be buffered.
func (bw *segmentBlockWriter) OpenBlock(kind string) (BlockStream, error) {
	if err := bw.alignTo8(); err != nil {
		return nil, err
	}
	bw.beginChecksum()
	return &blockStream{bw: bw, kind: kind, offset: *bw.off}, nil
}

// blockStream is a single in-progress block owned by one segmentBlockWriter.
type blockStream struct {
	bw     *segmentBlockWriter
	kind   string
	offset uint64
	closed bool
}

func (s *blockStream) Write(p []byte) (int, error) {
	if s.closed {
		return 0, fmt.Errorf("blockStream: write after close")
	}
	before := *s.bw.off
	err := s.bw.writeBytes(p)
	return int(*s.bw.off - before), err
}

func (s *blockStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	bw := s.bw
	sum := bw.finishChecksum()
	name := bw.field + "_" + s.kind
	bw.blockIndex[name] = BlockDef{
		Field:  bw.field,
		Kind:   s.kind,
		Offset: s.offset,
		Size:   *bw.off - s.offset,
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
