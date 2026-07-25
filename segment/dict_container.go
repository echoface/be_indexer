package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// DictContainer is the default ContainerReader for fields that use
// FlatDict + FlatPostingList. It performs dictionary binary search and
// zero-copy posting list access. The dict is immutable and set once
// during segment loading.
type DictContainer struct {
	dict *FlatDict
}

func (c DictContainer) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("dict container: query must be string, got %T", query)
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
