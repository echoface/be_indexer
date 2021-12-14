package roaringidx

import (
	"bytes"
	"fmt"

	"github.com/echoface/be_indexer"
	cedar "github.com/iohub/ahocorasick"
)

type (
	ACBEContainer struct {
		wc  PostingList
		inc *cedar.Matcher
		exc *cedar.Matcher
	}

	// ACBEContainerBuilder implement BEContainerBuilder interface
	ACBEContainerBuilder struct {
		container *ACBEContainer
		incValues map[string][]ConjunctionID
		excValues map[string][]ConjunctionID
	}
)

func NewACBEContainerBuilder(_ FieldSetting) BEContainerBuilder {
	return &ACBEContainerBuilder{
		container: NewACBEContainer(),
		incValues: map[string][]ConjunctionID{},
		excValues: map[string][]ConjunctionID{},
	}
}

func NewACBEContainer() *ACBEContainer {
	return &ACBEContainer{
		wc:  NewPostingList(),
		inc: nil,
		exc: nil,
	}
}

func (c *ACBEContainer) AddWildcard(id ConjunctionID) {
	c.wc.Add(uint64(id))
}

func (c *ACBEContainer) Retrieve(values be_indexer.Values, inout *PostingList) error {
	inout.Or(c.wc.Bitmap)

	if len(values) == 0 { // empty assign
		return nil
	}

	textData := bytes.NewBuffer(nil)
	for _, vi := range values {
		if str, ok := vi.(string); ok {
			textData.WriteString(str)
			continue
		}
		return fmt.Errorf("query assign:%+v not string type", vi)
	}

	rawContent := textData.Bytes()
	resp := c.inc.Match(rawContent)
	defer resp.Release()

	for resp.HasNext() {
		items := resp.NextMatchItem(rawContent)
		for _, itr := range items {
			// key := c.inc.Key(rawContent, itr)
			inout.Or(itr.Value.(PostingList).Bitmap)
		}
	}

	excResp := c.exc.Match(rawContent)
	defer excResp.Release()
	for excResp.HasNext() {
		items := excResp.NextMatchItem(rawContent)
		for _, itr := range items {
			// key := c.inc.Key(rawContent, itr)
			inout.AndNot(itr.Value.(PostingList).Bitmap)
		}
	}
	return nil
}

// EncodeWildcard equal to: EncodeExpr(id ConjunctionID, nil)
func (builder *ACBEContainerBuilder) EncodeWildcard(id ConjunctionID) {
	builder.container.AddWildcard(id)
}

func (builder *ACBEContainerBuilder) EncodeExpr(id ConjunctionID, expr *be_indexer.BoolExprs) error {
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
			builder.incValues[kw] = append(builder.incValues[kw], id)
		} else {
			builder.excValues[kw] = append(builder.excValues[kw], id)
		}
	}

	if !expr.Incl {
		builder.container.AddWildcard(id)
	}
	return nil
}

func (builder *ACBEContainerBuilder) BuildBEContainer() (BEContainer, error) {
	incMatcher := cedar.NewMatcher()
	for kw, ids := range builder.incValues {
		pl := NewPostingList()
		if len(ids) == 0 {
			panic(fmt.Errorf("empty posting list not allowed"))
		}
		for _, id := range ids {
			pl.Add(uint64(id))
		}
		// pl.RunOptimize() // NOTE: after testing, this make retrieve slower x4
		incMatcher.Insert([]byte(kw), pl)
	}
	incMatcher.Compile()

	excMatcher := cedar.NewMatcher()
	for kw, ids := range builder.excValues {
		pl := NewPostingList()
		if len(ids) == 0 {
			panic(fmt.Errorf("empty posting list not allowed"))
		}
		for _, id := range ids {
			pl.Add(uint64(id))
		}
		// pl.RunOptimize() // NOTE: after testing, this make retrieve slower x4
		excMatcher.Insert([]byte(kw), pl)
	}
	excMatcher.Compile()

	builder.container.inc = incMatcher
	builder.container.exc = excMatcher
	// builder.container.wc.RunOptimize() // NOTE: this make retrieve slower

	return builder.container, nil
}
