package parser

import (
	"fmt"
	"hash/fnv"
)

type (
	StrHashParser struct{}
)

func NewStrHashParser() FieldValueParser {
	return &StrHashParser{}
}

func (p *StrHashParser) ParseAssign(v interface{}) ([]uint64, error) {
	switch str := v.(type) {
	case string:
		h := fnv.New64()
		if n, e := h.Write([]byte(str)); n != len(str) || e != nil {
			return nil, fmt.Errorf("fnv hash fail:%s", str)
		}
		return []uint64{h.Sum64()}, nil
	default:
		return nil, fmt.Errorf("v:%+v not a string", v)
	}
}

// ParseValue parse bool expression value into id-encoded ids
func (p *StrHashParser) ParseValue(v interface{}) ([]uint64, error) {
	return p.ParseAssign(v)
}
