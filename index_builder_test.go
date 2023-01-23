package be_indexer

import (
	"fmt"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

type (
	TestBuilderCacheImpl struct {
		data map[ConjID][]byte
	}
)

func (c *TestBuilderCacheImpl) Reset() {
	c.data = map[ConjID][]byte{}
}
func (c *TestBuilderCacheImpl) Get(conjID ConjID) ([]byte, bool) {
	content, found := c.data[conjID]
	fmt.Printf("get cache hit:%t data size:%d\n", found, len(content))
	return content, found
}
func (c *TestBuilderCacheImpl) Set(conjID ConjID, data []byte) {
	fmt.Printf("set cache for:%d data size:%d\n", conjID, len(data))
	c.data[conjID] = data
}

func TestParseCache(t *testing.T) {
	convey.Convey("test parse cache", t, func() {
		b := NewCompactIndexerBuilder(WithCacheProvider(&TestBuilderCacheImpl{
			data: map[ConjID][]byte{},
		}))

		doc := NewDocument(12)
		var values []int64
		for i := int64(0); i < 1000; i++ {
			values = append(values, i)
		}
		doc.AddConjunction(NewConjunction().In("tag", values))

		convey.So(b.AddDocument(doc), convey.ShouldBeNil)
		_ = b.BuildIndex()

		doc2 := NewDocument(13)
		doc2.AddConjunction(NewConjunction().In("age", values))

		convey.So(b.AddDocument(doc, doc2), convey.ShouldBeNil)
		_ = b.BuildIndex()
	})
}
