package roaringidx

import (
	"fmt"

	"github.com/echoface/be_indexer/util"

	aho "github.com/anknown/ahocorasick"
	"github.com/echoface/be_indexer"
)

type (
	ACBEContainer struct {
		meta *FieldMeta

		wc PostingList

		inc *aho.Machine

		exc *aho.Machine

		incValues map[string]PostingList
		excValues map[string]PostingList
	}

	// ACBEContainerBuilder implement BEContainerBuilder interface
	ACBEContainerBuilder struct {
		container *ACBEContainer
	}
)

func NewACBEContainerBuilder(meta *FieldMeta) BEContainerBuilder {
	util.PanicIf(meta == nil, "nil FieldMeta is not allowed")
	return &ACBEContainerBuilder{
		container: NewACBEContainer(meta),
	}
}

func NewACBEContainer(meta *FieldMeta) *ACBEContainer {
	return &ACBEContainer{
		meta:      meta,
		wc:        NewPostingList(),
		inc:       nil,
		exc:       nil,
		incValues: map[string]PostingList{},
		excValues: map[string]PostingList{},
	}
}

func (c *ACBEContainer) Meta() *FieldMeta {
	return c.meta
}

func (c *ACBEContainer) AddWildcard(id ConjunctionID) {
	c.wc.Add(uint64(id))
}

func (c *ACBEContainer) AddIncludeID(key string, id ConjunctionID) {
	pl, ok := c.incValues[key]
	if !ok {
		pl = NewPostingList()
		c.incValues[key] = pl
	}
	pl.Add(uint64(id))
}

func (c *ACBEContainer) AddExcludeID(key string, id ConjunctionID) {
	pl, ok := c.excValues[key]
	if !ok {
		pl = NewPostingList()
		c.excValues[key] = pl
	}
	pl.Add(uint64(id))
}

func (c *ACBEContainer) Retrieve(values be_indexer.Values, inout *PostingList) error {
	inout.Or(c.wc.Bitmap)

	if len(values) == 0 { // empty assign
		return nil
	}

	data := make([]rune, 0, len(values)*4)
	for _, vi := range values {
		if str, ok := vi.(string); ok {
			data = append(data, []rune(str)...)
			continue
		}
		return fmt.Errorf("query assign:%+v not string type", vi)
	}

	if c.inc != nil {
		terms := c.inc.MultiPatternSearch(data, false)
		for _, term := range terms {
			// key := c.inc.Key(rawContent, itr)
			inout.Or(c.incValues[string(term.Word)].Bitmap)
		}
	}

	if c.exc != nil {
		terms := c.exc.MultiPatternSearch(data, false)
		for _, term := range terms {
			// key := c.inc.Key(rawContent, itr)
			inout.AndNot(c.excValues[string(term.Word)].Bitmap)
		}
	}
	return nil
}

// EncodeWildcard equal to: EncodeExpr(id ConjunctionID, nil)
func (builder *ACBEContainerBuilder) EncodeWildcard(id ConjunctionID) {
	builder.container.AddWildcard(id)
}

func (builder *ACBEContainerBuilder) EncodeExpr(id ConjunctionID, expr *be_indexer.BooleanExpr) error {
	if expr == nil {
		builder.container.AddWildcard(id)
		return nil
	}

	var ok bool
	var kw string
	for _, value := range expr.Value {
		if kw, ok = value.(string); !ok {
			return fmt.Errorf("not supported none string value")
		}
		if expr.Incl {
			builder.container.AddIncludeID(kw, id)
		} else {
			builder.container.AddExcludeID(kw, id)
		}
	}
	if !expr.Incl {
		builder.container.AddWildcard(id)
	}
	return nil
}

func (builder *ACBEContainerBuilder) BuildBEContainer() (BEContainer, error) {
	var err error
	keys := make([][]rune, 0, len(builder.container.incValues))
	if len(builder.container.incValues) > 0 {
		for kw, _ := range builder.container.incValues {
			keys = append(keys, []rune(kw))
		}
		builder.container.inc = &aho.Machine{}
		if err = builder.container.inc.Build(keys); err != nil {
			return nil, err
		}
	}

	if len(builder.container.excValues) > 0 {
		keys = keys[:0]
		for kw, _ := range builder.container.excValues {
			keys = append(keys, []rune(kw))
		}
		builder.container.exc = &aho.Machine{}
		if err = builder.container.exc.Build(keys); err != nil {
			return nil, err
		}
	}
	return builder.container, nil
}

func (builder *ACBEContainerBuilder) NeedParser() bool {
	return false
}
