package parser

import (
	"fmt"
	"hash/fnv"

	"github.com/echoface/be_indexer/util"
)

type (
	StrHashParser struct{}
)

func NewStrHashParser() ValueIDGenerator {
	return &StrHashParser{}
}

func (p *StrHashParser) Name() string {
	return "number_range"
}

func (p *StrHashParser) ParseAssign(v interface{}) ([]uint64, error) {
	if util.NilInterface(v) {
		return nil, nil
	}
	return p.ParseValue(v)
}

// ParseValue parse bool expression value into id-encoded ids
func (p *StrHashParser) ParseValue(v interface{}) ([]uint64, error) {
	switch str := v.(type) {
	case string:
		h := fnv.New64()
		if n, e := h.Write([]byte(str)); n != len(str) || e != nil {
			return nil, fmt.Errorf("fnv hash fail:%s", str)
		}
		return []uint64{h.Sum64()}, nil
	case []string:
		result := make([]uint64, 0, len(str))
		for _, s := range str {
			h := fnv.New64()
			if n, e := h.Write([]byte(s)); n != len(s) || e != nil {
				return nil, fmt.Errorf("fnv hash fail:%s", str)
			}
			result = append(result, h.Sum64())
		}
		return result, nil
	case []interface{}:
		result := make([]uint64, 0, len(str))
		var ok bool
		var strValue string
		for _, s := range str {
			if strValue, ok = s.(string); !ok {
				return nil, fmt.Errorf("v:%+v not a string", v)
			}
			h := fnv.New64()
			if n, e := h.Write([]byte(strValue)); n != len(strValue) || e != nil {
				return nil, fmt.Errorf("fnv hash fail:%s", str)
			}
			result = append(result, h.Sum64())
		}
		return result, nil
	default:
		return nil, fmt.Errorf("v:%+v not a string", v)
	}
}
