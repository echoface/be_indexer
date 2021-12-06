package v2

import "fmt"

/*parser 解析指定特殊格式的Value,并通过IDAllocator将ValueID化*/

const (
	// inner register parser can't be override, customized parser can't use prefix "#"
	ParserNamerNumber  = "#number"
	ParserNameNubRange = "#num_range"
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
	factory[ParserNamerNumber] = NewNumberParser
	factory[ParserNameNubRange] = NewNumRangeParser
	factory[ParserNameStrHash] = NewStrHashParser
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
