package parser

/*parser 解析指定特殊格式的Value,并通过IDAllocator将ValueID化*/

var (
	factory map[string]Builder
)

type (
	Builder func(allocator IDAllocator) FieldValueParser
)

func init() {
	factory = make(map[string]Builder)

	factory["common"] = NewCommonStrParser
}
