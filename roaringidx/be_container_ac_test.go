package roaringidx

import (
	"fmt"
	"testing"

	cedar "github.com/iohub/ahocorasick"

	"github.com/echoface/be_indexer/util"

	aho "github.com/anknown/ahocorasick"
)

func TestACBEContainer_AddIncludeID(t *testing.T) {
	m := &aho.Machine{}

	data := []string{
		"字典",
		"中国字典",
		"中国子",
		"中",
		"典",
		"dict",
	}
	m2 := cedar.NewMatcher()
	for i, v := range data {
		m2.Insert([]byte(v), i)
	}
	m2.Compile()

	e := m.Build([][]rune{
		[]rune("字典"),
		[]rune("中国字典"),
		[]rune("中国子"),
		[]rune("中"),
		[]rune("典"),
		[]rune("dict"),
	})
	util.PanicIfErr(e, "build fail")

	q := []rune("中华人名共和国，中国字典dict的中哎字典")

	rs := m.MultiPatternSearch(q, false)
	for _, r := range rs {
		fmt.Println("not-immediately:", r.Pos, string(r.Word))
	}

	rs = m.MultiPatternSearch(q, true)
	for _, r := range rs {
		fmt.Println("immediately:", r.Pos, string(r.Word))
	}

	buf := util.RunesToBytes(q)
	resp := m2.Match(buf)
	for resp.HasNext() {
		items := resp.NextMatchItem(buf)
		for _, itr := range items {
			fmt.Println("cedar:", string(m2.Key(buf, itr)))
		}
	}
	resp.Release()

	for _, v := range util.RunesToBytes([]rune("一")) {
		fmt.Println("value:", int(v))
	}
}
