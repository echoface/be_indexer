package be_indexer

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestValues_Int(t *testing.T) {
	convey.Convey("test Int factory function", t, func() {
		// 测试各种 int 类型
		var v1 int = 100
		var v2 int8 = 8
		var v3 int16 = 16
		var v4 int32 = 32
		var v5 int64 = 64
		var v6 uint = 100
		var v7 uint8 = 8
		var v8 uint16 = 16

		// 所有类型都应该能转换为 IntValue
		convey.So(Int(v1), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v2), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v3), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v4), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v5), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v6), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v7), convey.ShouldHaveSameTypeAs, IntValue(0))
		convey.So(Int(v8), convey.ShouldHaveSameTypeAs, IntValue(0))
	})
}

func TestValues_Str(t *testing.T) {
	convey.Convey("test Str factory function", t, func() {
		v := Str("hello")
		convey.So(v, convey.ShouldHaveSameTypeAs, StrValue(""))
		convey.So(string(v), convey.ShouldEqual, "hello")
	})
}

func TestValues_AsInt(t *testing.T) {
	convey.Convey("test AsInt conversion", t, func() {
		// 测试 []int64
		ints := []int64{1, 2, 3}
		result, ok := AsInt(ints)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)

		// 测试 []int
		ints32 := []int{4, 5, 6}
		result, ok = AsInt(ints32)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)

		// 测试空值
		result, ok = AsInt(nil)
		convey.So(ok, convey.ShouldBeFalse)
	})
}

func TestValues_AsStr(t *testing.T) {
	convey.Convey("test AsStr conversion", t, func() {
		// 测试 []string
		strs := []string{"a", "b", "c"}
		result, ok := AsStr(strs)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)

		// 测试 []StrValue
		strVals := []StrValue{"x", "y", "z"}
		result, ok = AsStr(strVals)
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)
	})
}

func TestValues_ParseInts(t *testing.T) {
	convey.Convey("test ParseInts", t, func() {
		result, ok := ParseInts("1,2,3")
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)

		// 测试带空格的
		result, ok = ParseInts("1, 2, 3")
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(len(result), convey.ShouldEqual, 3)

		// 测试无效输入
		result, ok = ParseInts("abc")
		convey.So(ok, convey.ShouldBeFalse)
	})
}
