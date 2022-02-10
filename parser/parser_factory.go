package parser

/*parser 解析指定特殊格式的Value,并通过IDAllocator将ValueID化*/

const (
	// ParserNameNumber inner register parser can't override, customized parser can't use prefix "#"
	ParserNameNumber   = "#number"
	ParserNameCommon   = "#common"
	ParserNameNumRange = "#num_range"
	ParserNameStrHash  = "#str_hash"
)
