// Package mph provides a minimal-perfect-hash (CHD) dictionary container
// that replaces FlatDict binary search with O(1) lookup for exact-term fields.
//
// Users opt in per-field via FieldMeta.FieldOption{Container: "mph_dict"}.
// No custom encoder is needed — the default ExactTermEncoder handles value
// encoding, and the engine routes queries through the mph container when
// the field's Container is "mph_dict".
//
// The mph table maps term strings to PostingRef values; it is built at segment
// construction using the Compress-Hash-Displace algorithm and loaded at query
// time via zero-copy mmap.
package mph

import (
	"github.com/echoface/be_indexer/segment"
)

const ContainerName = "mph_dict"

func init() {
	segment.RegisterContainer(ContainerName, segment.ContainerDef{
		Reader:  newReaderFactory,
		Builder: newBuilderFactory,
	})
}
