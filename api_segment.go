package be_indexer

import "github.com/echoface/be_indexer/segment"

type (
	SegmentReader          = segment.SegmentReader
	SegmentReaderOptions   = segment.ReaderOptions
	OpenSegmentFileOptions = segment.OpenFileOptions
	BlockChecksumMode      = segment.BlockChecksumMode
)

const (
	SegmentVersionV5      = segment.SegmentVersionV5
	BlockChecksumOnOpen   = segment.BlockChecksumOnOpen
	BlockChecksumDisabled = segment.BlockChecksumDisabled
)

// NewSegmentReader validates immutable caller-owned bytes and borrows their storage.
func NewSegmentReader(data []byte) (*SegmentReader, error) { return segment.NewSegmentReader(data) }

// NewSegmentReaderWithOptions configures checksum verification. Structural checks
// always run; disabling hashing requires separately verified immutable artifacts.
func NewSegmentReaderWithOptions(data []byte, opts SegmentReaderOptions) (*SegmentReader, error) {
	return segment.NewSegmentReaderWithOptions(data, opts)
}

// OpenSegmentFile maps an immutable file read-only. Close after all users of its views finish.
func OpenSegmentFile(path string, opts OpenSegmentFileOptions) (*SegmentReader, error) {
	return segment.OpenSegmentFile(path, opts)
}
