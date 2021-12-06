package bitsinvt

import "github.com/echoface/be_indexer"

type (
	BEContainer interface {
		EncodeExpr(expr be_indexer.BoolExprs) error
		Build() error
	}

	// BSIBEContainer a bit-slice indexing based index bitmap container
	BSIBEContainer struct {
	}

	// CommonBEContainer a common value based inverted index bitmap container
	CommonBEContainer struct {
	}
)

func NewCommonBEContainer() *CommonBEContainer {
	return &CommonBEContainer{}
}

func (c *CommonBEContainer) EncodeExpr(expr be_indexer.BoolExprs) error {
	return nil
}

func (c *CommonBEContainer) Build() error {
	return nil
}

func NewBSIBEContainer() *BSIBEContainer {
	return &BSIBEContainer{}
}

func (bsi *BSIBEContainer) EncodeExpr(expr be_indexer.BoolExprs) error {
	return nil
}

func (bsi *BSIBEContainer) Build() error {
	return nil
}
