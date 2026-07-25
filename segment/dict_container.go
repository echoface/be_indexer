package segment

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
)

func init() {
	RegisterContainer(core.IndexNameDefault, ContainerDef{
		Reader: func(_ []byte) (ContainerReader, error) {
			return DictContainer{}, nil
		},
		// Builder is nil: default dict uses the framework's built-in
		// FlatDict + FlatPostingList path during segment construction.
	})
}

// DictContainer is the default ContainerReader for fields that use
// FlatDict + FlatPostingList. It performs dictionary binary search and
// zero-copy posting list access.
type DictContainer struct{}

func (DictContainer) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("dict container: query must be string, got %T", query)
	}
	if ctx.Dict == nil {
		return nil, nil
	}
	ref, found := ctx.Dict.Find([]byte(term))
	if !found {
		return nil, nil
	}
	pl, err := NewPostingListAt(ctx.Pl, ref)
	if err != nil {
		return nil, fmt.Errorf("field %s: %w", field, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}
