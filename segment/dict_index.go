package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// DictIndex is the default IndexReader for fields that use
// FlatDict + FlatPostingList. It performs dictionary binary search and
// zero-copy posting list access.
type DictIndex struct {
	dict *FlatDict
}

// NewDictReader creates a DictIndex backed by the given FlatDict.
func NewDictReader(dict *FlatDict) DictIndex {
	return DictIndex{dict: dict}
}

func (c DictIndex) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("dict index: query must be string, got %T", query)
	}
	if c.dict == nil {
		return nil, nil
	}
	ref, found := c.dict.Find([]byte(term))
	if !found {
		return nil, nil
	}
	pl, err := NewPostingListAt(ctx.Pl, ref)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}

func init() {
	RegisterIndex(core.IndexNameDefault, IndexDef{
		Builder: func(env BuilderEnv) IndexBuilder { return NewDictBuilder(env) },
	})
}

// DictBuilder implements IndexBuilder for the default term-posting
// path. It accumulates term → EntryID mappings via a KeyedPostingCollector
// and writes FlatDict + FlatPostingList blocks at Build time.
type DictBuilder struct {
	collector *KeyedPostingCollector
}

func NewDictBuilder(env BuilderEnv) IndexBuilder {
	return &DictBuilder{
		collector: NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
}

func (b *DictBuilder) AddRecord(record any, entries []core.EntryID) error {
	term, ok := record.(string)
	if !ok {
		return fmt.Errorf("DictBuilder: expected string record, got %T", record)
	}
	return b.collector.Add([]byte(term), entries)
}

func (b *DictBuilder) Build(bw BlockWriter) error {
	dict := map[string]PostingRef{}
	n, err := b.collector.WritePostings(bw, func(key []byte, ref PostingRef) error {
		dict[string(key)] = ref
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	dictBytes, err := WriteFlatDict(dict)
	if err != nil {
		return err
	}
	return bw.WriteBlock(BlockKindDict, dictBytes)
}
