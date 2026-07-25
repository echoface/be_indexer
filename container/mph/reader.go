package mph

import (
	"encoding/binary"
	"fmt"

	"github.com/alecthomas/mph"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newReaderFactory(b []byte) (segment.ContainerReader, error) {
	return NewReader(b)
}

type Reader struct {
	chd *mph.CHD
}

func NewReader(b []byte) (*Reader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("mph: blob too short: len=%d", len(b))
	}
	chd, err := mph.Mmap(b)
	if err != nil {
		return nil, fmt.Errorf("mph: mmap CHD: %w", err)
	}
	return &Reader{chd: chd}, nil
}

func decodePostingRef(val []byte) (segment.PostingRef, error) {
	if len(val) < 12 {
		return segment.PostingRef{}, fmt.Errorf("mph: corrupted posting ref: len=%d", len(val))
	}
	return segment.PostingRef{
		Offset: binary.LittleEndian.Uint64(val[0:8]),
		Count:  binary.LittleEndian.Uint32(val[8:12]),
	}, nil
}

func (r *Reader) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("mph: query must be string, got %T", query)
	}
	val := r.chd.Get([]byte(term))
	if val == nil {
		return nil, nil
	}
	ref, err := decodePostingRef(val)
	if err != nil {
		return nil, err
	}
	pl, err := segment.NewPostingListAt(ctx.Pl, ref)
	if err != nil {
		return nil, fmt.Errorf("mph: term %q posting: %w", term, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}
