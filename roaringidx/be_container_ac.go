package roaringidx

import (
	"fmt"

	"github.com/echoface/be_indexer/util"
	aho "github.com/anknown/ahocorasick"
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
)

type (
	ACBEContainer struct {
		querySep string

		meta *FieldMeta

		wc PostingList

		inc *aho.Machine

		exc *aho.Machine

		incValues map[string]PostingList
		excValues map[string]PostingList
	}
)

const (
	DefaultACContainerQueryJoinSep = " "
)

func NewACBEContainer(meta *FieldMeta, sep string) *ACBEContainer {
	util.PanicIf(meta == nil, "nil FieldMeta is not allowed")

	return &ACBEContainer{
		querySep:  sep,
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
			data = append(data, []rune(c.querySep)...)
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
func (c *ACBEContainer) EncodeWildcard(id ConjunctionID) {
	c.AddWildcard(id)
}

func (c *ACBEContainer) EncodeExpr(id ConjunctionID, expr *be_indexer.BooleanExpr) error {
	if expr == nil {
		c.AddWildcard(id)
		return nil
	}

	var ok bool
	var kw string
	for _, value := range expr.Value {
		if kw, ok = value.(string); !ok {
			return fmt.Errorf("not supported none string value")
		}
		if expr.Incl {
			c.AddIncludeID(kw, id)
		} else {
			c.AddExcludeID(kw, id)
		}
	}
	if !expr.Incl {
		c.AddWildcard(id)
	}
	return nil
}

func (c *ACBEContainer) BuildBEContainer() (BEContainer, error) {
	var err error
	keys := make([][]rune, 0, len(c.incValues))
	if len(c.incValues) > 0 {
		for kw, _ := range c.incValues {
			keys = append(keys, []rune(kw))
		}
		c.inc = &aho.Machine{}
		if err = c.inc.Build(keys); err != nil {
			return nil, err
		}
	}

	if len(c.excValues) > 0 {
		keys = keys[:0]
		for kw, _ := range c.excValues {
			keys = append(keys, []rune(kw))
		}
		c.exc = &aho.Machine{}
		if err = c.exc.Build(keys); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *ACBEContainer) NeedParser() bool {
	return false
}
