package parser

import (
	"fmt"
	"testing"
)

func TestCommonNumberParser_ParseValue(t *testing.T) {
	parser := NewNumberParser()

	r, e := parser.ParseValue([]int{1, 2, 3})
	fmt.Println(r, e)

	r, e = parser.ParseValue([]uint8{1, 2, 3})
	fmt.Println(r, e)

	r, e = parser.ParseValue([]string{"gonghuan", "hello"})
	fmt.Println("result:", r, e)

	r, e = parser.ParseValue("gonghuan")
	fmt.Println("result:", r, e)

	r, e = parser.ParseValue(12)
	fmt.Println("result:", r, e)

	r, e = parser.ParseValue([3]int64{1, 2, 3})
	fmt.Println("result:", r, e)

	r, e = parser.ParseValue([3]string{"gonghuan", "hello", "1"})
	fmt.Println("result:", r, e)
}
