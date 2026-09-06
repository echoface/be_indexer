package segment

// MagicNumber identifies the only supported Segment format (v4).
// v4 merges all K-groups into per-field blocks (K is encoded in EntryID itself).
var MagicNumber = []byte("BEIDX\x00\x00\x04")

const (
	SegmentVersionV4 = 4

	wildcardsBlockName = "__wildcards"
	checksumPrefix     = "sha256:"
)

// Block kind discriminators stored in BlockDef.Kind.
const (
	BlockKindPostings  = "postings"
	BlockKindDict      = "dict"
	BlockKindAC        = "ac_matcher"
	BlockKindRange     = "ext_range"
	BlockKindWildcards = "wildcards"
)

// MetaBlock defines the immutable segment footer metadata. Data blocks are
// stored before the JSON footer; the final 8 bytes of the segment point to this
// metadata block.
//
// BlockIndex maps a block name to a structured BlockDef carrying its (K, field,
// kind), byte range and checksum inline. The structured fields let the reader
// resolve blocks directly instead of parsing the block name string, and fold
// the former separate BlockChecksums map into each BlockDef. The map is still
// keyed by name so json.Marshal emits deterministic (key-sorted) bytes,
// guaranteeing identical output across the in-memory and external builders.
type MetaBlock struct {
	Version        int                 `json:"version"`
	DocCount       int                 `json:"doc_count"`
	SchemaHash     string              `json:"schema_hash,omitempty"`
	Fields         []FieldMetaDump     `json:"fields"`
	BlockIndex     map[string]BlockDef `json:"block_index"`
	WildcardsBlock string              `json:"wildcards_block,omitempty"`
}

type FieldMetaDump struct {
	Name      string `json:"name"`
	ID        uint64 `json:"id"`
	IndexType string `json:"index_type"`
}

// BlockDef describes one data block. Field/Kind are the structured identity
// (Field is empty for the wildcards block), Offset/Size locate it, and
// Checksum (sha256:...) protects its bytes.
type BlockDef struct {
	Field    string `json:"field,omitempty"`
	Kind     string `json:"kind"`
	Offset   uint64 `json:"offset"`
	Size     uint64 `json:"size"`
	Checksum string `json:"checksum,omitempty"`
}
