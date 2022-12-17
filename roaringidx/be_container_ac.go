package roaringidx

import (
	"fmt"
	"strings"

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

func (c *ACBEContainer) buildPatternQueryContent(v be_indexer.Values) ([]rune, error) {
	data := make([]rune, 0, 64)
	switch tv := v.(type) {
	case string:
		data = []rune(tv)
	case []string:
		data = []rune(strings.Join(tv, c.querySep))
	case []interface{}:
		for idx, vi := range tv {
			if str, ok := vi.(string); !ok {
				return nil, fmt.Errorf("query assign:%+v not string type", v)
			} else {
				if idx == 0 {

					data = append(data, []rune(str)...)
				} else {
					data = append(data, []rune(c.querySep)...)
					data = append(data, []rune(str)...)
				}
			}
		}
	default:
		return nil, fmt.Errorf("query assign:%+v not string type", v)
	}
	return data, nil
}

func (c *ACBEContainer) Retrieve(values be_indexer.Values, inout *PostingList) error {
	inout.Or(c.wc.Bitmap)

	if util.NilInterface(values) { // empty assign
		return nil
	}
	data, err := util.BuildAcMatchContent(values, c.querySep)
	if err != nil {
		return err
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
	if expr == nil || util.NilInterface(expr.Value) {
		c.AddWildcard(id)
		return nil
	}
	util.PanicIf(expr.Operator != be_indexer.ValueOptEQ, "ac_match support EQ operator only")

	keys, err := util.ParseAcMatchDict(expr.Value)
	if err != nil {
		return fmt.Errorf("ac container need string type values, err:%v", err)
	}
	for _, v := range keys {
		if expr.Incl {
			c.AddIncludeID(v, id)
		} else {
			c.AddExcludeID(v, id)
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
		for kw := range c.incValues {
			keys = append(keys, []rune(kw))
		}
		c.inc = &aho.Machine{}
		if err = c.inc.Build(keys); err != nil {
			return nil, err
		}
	}

	if len(c.excValues) > 0 {
		keys = keys[:0]
		for kw := range c.excValues {
			keys = append(keys, []rune(kw))
		}
		c.exc = &aho.Machine{}
		if err = c.exc.Build(keys); err != nil {
			return nil, err
		}
	}
	return c, nil
}
