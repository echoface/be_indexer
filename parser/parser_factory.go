package parser

import (
	"fmt"
)

/*parser 解析指定特殊格式的Value,并通过IDAllocator将ValueID化*/

const (
	// ParserNameNumber inner register parser can't override, customized parser can't use prefix "#"
	ParserNameNumber   = "#number"
	ParserNameCommon   = "#common"
	ParserNameNumRange = "#num_range"
	ParserNameStrHash  = "#str_hash"
)

var (
	factory map[string]Builder
)

type (
	Builder func() FieldValueParser
)

func init() {
	factory = make(map[string]Builder)

	RegisterBuilder(ParserNameCommon, NewCommonStrParser)
	RegisterBuilder(ParserNameNumber, NewNumberParser)
	RegisterBuilder(ParserNameNumRange, NewNumRangeParser)
	RegisterBuilder(ParserNameStrHash, NewStrHashParser)
}

// RegisterBuilder register override other will panic to avoid wrong value id be use in indexing
func RegisterBuilder(name string, builder Builder) {
	if _, ok := factory[name]; ok {
		panic(fmt.Errorf("name:%s has been register before", name))
	}
	factory[name] = builder
}

func HasParser(name string) (ok bool) {
	_, ok = factory[name]
	return ok
}

func NewParser(name string) FieldValueParser {
	var ok bool
	var builderFn Builder
	if builderFn, ok = factory[name]; !ok {
		return nil
	}
	return builderFn()
}
