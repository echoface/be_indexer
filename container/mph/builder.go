package mph

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/alecthomas/mph"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newBuilderFactory() segment.ContainerBuilder {
	return NewMPHBuilder()
}

// NewMPHBuilder creates a fresh MPHBuilder.
func NewMPHBuilder() *MPHBuilder {
	return &MPHBuilder{}
}

// MPHBuilder accumulates (term, PostingRef) pairs and serializes them
// into a CHD minimal-perfect-hash block.
type MPHBuilder struct {
	terms []termRef
}

type termRef struct {
	term string
	ref  segment.PostingRef
}

func (b *MPHBuilder) RecordToKey(record any) []byte {
	switch v := record.(type) {
	case string:
		return []byte(v)
	case []byte:
		return v
	default:
		return nil
	}
}

func (b *MPHBuilder) AddKeyedPosting(key []byte, ref segment.PostingRef, entries []core.EntryID) error {
	b.terms = append(b.terms, termRef{term: string(key), ref: ref})
	return nil
}

func encodePostingRef(ref segment.PostingRef) []byte {
	var buf [12]byte
	binary.LittleEndian.PutUint64(buf[0:8], ref.Offset)
	binary.LittleEndian.PutUint32(buf[8:12], ref.Count)
	return buf[:]
}

func (b *MPHBuilder) Build() ([]byte, error) {
	if len(b.terms) == 0 {
		return nil, nil
	}
	chdBuilder := mph.Builder()
	for _, t := range b.terms {
		chdBuilder.Add([]byte(t.term), encodePostingRef(t.ref))
	}
	chd, err := chdBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("mph: build CHD: %w", err)
	}
	var buf bytes.Buffer
	if err := chd.Write(&buf); err != nil {
		return nil, fmt.Errorf("mph: serialize CHD: %w", err)
	}
	return buf.Bytes(), nil
}
