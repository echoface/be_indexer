package parser

import (
	"fmt"
	"testing"
)

func TestCommonStrParser_ParseValue(t *testing.T) {
	idGen := NewIDAllocatorImpl()
	parser := NewCommonStrParser(idGen)

	r, e := parser.ParseValue([]int{1, 2, 3})
	fmt.Println(r, e)

	r, e = parser.ParseValue([]uint8{1, 2, 3})
	fmt.Println(r, e)

	r, e = parser.ParseValue([]string{"gonghuan", "hello"})
	fmt.Println(r, e)

	r, e = parser.ParseValue("gonghuan")
	fmt.Println(r, e)

	r, e = parser.ParseValue(12)
	fmt.Println(r, e)

	r, e = parser.ParseValue([3]int64{1, 2, 3})
	fmt.Println(r, e)

	r, e = parser.ParseValue([3]string{"gonghuan", "hello", "1"})
	fmt.Println(r, e)
}
