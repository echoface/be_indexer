package parser

import "fmt"

/*parser 解析指定特殊格式的Value,并通过IDAllocator将ValueID化*/

const (
	// inner register parser can't be override, customized parser can't use prefix "#"
	CommonParser   = "#common"
	NumRangeParser = "#num_range"
)

var (
	factory map[string]Builder
)

type (
	Builder func(allocator IDAllocator) FieldValueParser
)

func init() {
	factory = make(map[string]Builder)
	factory[CommonParser] = NewCommonStrParser
	factory[NumRangeParser] = NewNumRangeParser
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

func NewParser(name string, idGen IDAllocator) FieldValueParser {
	builder, ok := factory[name]
	if !ok {
		return nil
	}
	return builder(idGen)
}
