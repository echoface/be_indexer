package mph

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/alecthomas/mph"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func newBuilderFactory(env segment.BuilderEnv) segment.IndexBuilder {
	return NewMPHBuilder(env)
}

type termRef struct {
	term string
	ref  segment.PostingRef
}

// MPHBuilder accumulates entries via AddRecord (full pipeline) or AddPosting (direct PostingRef).
type MPHBuilder struct {
	collector *segment.KeyedPostingCollector
	terms     []termRef // direct PostingRef references (test/advanced path)
}

func NewMPHBuilder(env segment.BuilderEnv) *MPHBuilder {
	return &MPHBuilder{
		collector: segment.NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
}

func (b *MPHBuilder) AddRecord(record any, entries []core.EntryID) error {
	term, ok := record.(string)
	if !ok {
		termBytes, ok2 := record.([]byte)
		if !ok2 {
			return fmt.Errorf("MPHBuilder: expected string or []byte record, got %T", record)
		}
		return b.collector.Add(termBytes, entries)
	}
	return b.collector.Add([]byte(term), entries)
}

func (b *MPHBuilder) AddPosting(term string, ref segment.PostingRef) {
	b.terms = append(b.terms, termRef{term: term, ref: ref})
}

func encodePostingRef(ref segment.PostingRef) []byte {
	var buf [12]byte
	binary.LittleEndian.PutUint64(buf[0:8], ref.Offset)
	binary.LittleEndian.PutUint32(buf[8:12], ref.Count)
	return buf[:]
}

func (b *MPHBuilder) Build(bw segment.BlockWriter) error {
	if b.collector != nil {
		_, err := segment.BuildPostings(b.collector, bw, func(key []byte, ref segment.PostingRef) error {
			b.terms = append(b.terms, termRef{term: string(key), ref: ref})
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(b.terms) == 0 {
		return nil
	}
	chdBuilder := mph.Builder()
	for _, t := range b.terms {
		chdBuilder.Add([]byte(t.term), encodePostingRef(t.ref))
	}
	chd, err := chdBuilder.Build()
	if err != nil {
		return fmt.Errorf("mph: build CHD: %w", err)
	}
	var buf bytes.Buffer
	if err := chd.Write(&buf); err != nil {
		return fmt.Errorf("mph: serialize CHD: %w", err)
	}
	return bw.WriteBlock(IndexName, buf.Bytes())
}
