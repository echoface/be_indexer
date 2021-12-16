package roaringidx

import (
	"fmt"
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

type (
	BEContainer interface {
		AddWildcard(id ConjunctionID)
		Retrieve(values be_indexer.Values, inout *PostingList) error
	}

	BEContainerBuilder interface {
		EncodeWildcard(id ConjunctionID) // equal to: EncodeExpr(id ConjunctionID, nil)

		EncodeExpr(id ConjunctionID, expr *be_indexer.BooleanExpr) error

		BuildBEContainer() (BEContainer, error)
	}

	// DefaultBEContainer a common value based inverted index bitmap container
	DefaultBEContainer struct {
		parser parser.FieldValueParser
		wc     PostingList
		inc    map[BEValue]PostingList
		exc    map[BEValue]PostingList
	}

	DefaultBEContainerBuilder struct {
		parser    parser.FieldValueParser
		container *DefaultBEContainer
	}
)

func NewDefaultBEContainerBuilder(setting FieldSetting) *DefaultBEContainerBuilder {
	valueParser := parser.NewParser(setting.Parser)
	if valueParser == nil {
		panic(fmt.Errorf("parser:%s not found", setting.Parser))
	}
	return &DefaultBEContainerBuilder{
		parser:    valueParser,
		container: NewDefaultBEContainer(valueParser),
	}
}

func NewDefaultBEContainer(parser parser.FieldValueParser) *DefaultBEContainer {
	return &DefaultBEContainer{
		parser: parser,
		wc:     NewPostingList(),
		inc:    map[BEValue]PostingList{},
		exc:    map[BEValue]PostingList{},
	}
}

func (c *DefaultBEContainer) AddWildcard(id ConjunctionID) {
	c.wc.Add(uint64(id))
}

func (c *DefaultBEContainer) AddInclude(value BEValue, id ConjunctionID) {
	pl, ok := c.inc[value]
	if !ok {
		pl = NewPostingList()
		c.inc[value] = pl
	}
	pl.Add(uint64(id))
}

func (c *DefaultBEContainer) AddExclude(value BEValue, id ConjunctionID) {
	pl, ok := c.exc[value]
	if !ok {
		pl = NewPostingList()
		c.exc[value] = pl
	}
	pl.Add(uint64(id))
	c.AddWildcard(id)
}

func (c *DefaultBEContainer) Retrieve(values be_indexer.Values, inout *PostingList) error {
	inout.Or(c.wc.Bitmap)

	if len(values) == 0 {
		return nil
	}

	fieldIDs := make([]uint64, 0, len(values))
	for _, vi := range values {
		ids, err := c.parser.ParseAssign(vi)
		if err != nil {
			return err
		}
		fieldIDs = append(fieldIDs, ids...)
	}
	for _, id := range fieldIDs {
		if incPl, ok := c.inc[BEValue(id)]; ok {
			inout.Or(incPl.Bitmap)
		}
	}
	for _, id := range fieldIDs {
		if excPl, ok := c.exc[BEValue(id)]; ok {
			inout.AndNot(excPl.Bitmap)
		}
	}
	return nil
}

func (builder *DefaultBEContainerBuilder) EncodeWildcard(id ConjunctionID) {
	builder.container.AddWildcard(id)
}

func (builder *DefaultBEContainerBuilder) EncodeExpr(id ConjunctionID, expr *be_indexer.BooleanExpr) error {
	if expr == nil {
		builder.EncodeWildcard(id)
	}
	for _, vi := range expr.Value {
		valueIDs, err := builder.parser.ParseValue(vi)
		if err != nil {
			return err
		}
		for _, value := range valueIDs {
			if expr.Incl {
				builder.container.AddInclude(BEValue(value), id)
			} else {
				builder.container.AddExclude(BEValue(value), id)
			}
		}
	}
	return nil
}

func (builder *DefaultBEContainerBuilder) BuildBEContainer() (BEContainer, error) {
	//for _, v := range builder.container.inc {
	//	v.RunOptimize()
	//}
	//for _, v := range builder.container.exc {
	//	v.RunOptimize()
	//}
	//builder.container.wc.RunOptimize()
	return builder.container, nil
}
