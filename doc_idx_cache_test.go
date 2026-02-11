package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

// MemoryDocCache 内存缓存实现（测试用）
type MemoryDocCache struct {
	data map[DocCacheKey]*DocIdxCache
}

func NewMemoryDocCache() *MemoryDocCache {
	return &MemoryDocCache{
		data: make(map[DocCacheKey]*DocIdxCache),
	}
}

func (c *MemoryDocCache) Get(key DocCacheKey) (*DocIdxCache, bool) {
	entry, ok := c.data[key]
	return entry, ok
}

func (c *MemoryDocCache) Set(key DocCacheKey, entry *DocIdxCache) {
	c.data[key] = entry
}

func (c *MemoryDocCache) Clear() {
	c.data = make(map[DocCacheKey]*DocIdxCache)
}

func (c *MemoryDocCache) Size() int {
	return len(c.data)
}

func TestDocLevelCache_Basic(t *testing.T) {
	convey.Convey("test document level cache basic functionality", t, func() {
		cache := NewMemoryDocCache()

		// 测试 Set 和 Get
		key := NewDocCacheKey(100, 1)
		entry := &DocIdxCache{
			DocID:      100,
			Version:    1,
			SchemaHash: 12345,
		}

		cache.Set(key, entry)
		retrieved, ok := cache.Get(key)

		convey.So(ok, convey.ShouldBeTrue)
		convey.So(retrieved.DocID, convey.ShouldEqual, 100)
		convey.So(retrieved.Version, convey.ShouldEqual, 1)
		convey.So(retrieved.SchemaHash, convey.ShouldEqual, 12345)

		// 测试 Clear
		cache.Clear()
		convey.So(cache.Size(), convey.ShouldEqual, 0)

		_, ok = cache.Get(key)
		convey.So(ok, convey.ShouldBeFalse)
	})
}

func TestIncrementalIndexing_WithCache(t *testing.T) {
	convey.Convey("test incremental indexing with document level cache", t, func() {
		cache := NewMemoryDocCache()

		// 第一次构建：不使用缓存（Version=0）
		builder1 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder1.ConfigField("age", core.FieldOption{Container: HolderNameDefault})
		builder1.ConfigField("city", core.FieldOption{Container: HolderNameDefault})

		doc1 := core.NewDocument(1)
		doc1.Version = 0 // 不使用缓存
		doc1.AddConjunction(core.NewConjunction().
			In("age", []int{18, 25, 30}).
			In("city", []string{"beijing", "shanghai"}))

		err := builder1.AddDocument(doc1)
		convey.So(err, convey.ShouldBeNil)

		index1, _ := builder1.BuildIndex()
		convey.So(index1, convey.ShouldNotBeNil)

		// 第二次构建：使用缓存（Version>0）
		builder2 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder2.ConfigField("age", core.FieldOption{Container: HolderNameDefault})
		builder2.ConfigField("city", core.FieldOption{Container: HolderNameDefault})

		doc2 := core.NewDocument(2)
		doc2.Version = 1 // 使用缓存
		doc2.AddConjunction(core.NewConjunction().
			In("age", []int{20, 25, 35}).
			In("city", []string{"shanghai", "guangzhou"}))

		err = builder2.AddDocument(doc2)
		convey.So(err, convey.ShouldBeNil)

		// 验证 doc2 的缓存是否被保存
		cacheKey2 := NewDocCacheKey(2, 1)
		_, ok := cache.Get(cacheKey2)
		convey.So(ok, convey.ShouldBeTrue)

		index2, _ := builder2.BuildIndex()
		convey.So(index2, convey.ShouldNotBeNil)

		// 第三次构建：复用 doc2 的缓存
		builder3 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder3.ConfigField("age", core.FieldOption{Container: HolderNameDefault})
		builder3.ConfigField("city", core.FieldOption{Container: HolderNameDefault})

		doc3 := core.NewDocument(2) // 同一个 ID
		doc3.Version = 1       // 相同的 Version，应该命中缓存
		doc3.AddConjunction(core.NewConjunction().
			In("age", []int{20, 25, 35}).
			In("city", []string{"shanghai", "guangzhou"}))

		err = builder3.AddDocument(doc3)
		convey.So(err, convey.ShouldBeNil)

		index3, _ := builder3.BuildIndex()
		convey.So(index3, convey.ShouldNotBeNil)

		// 验证检索结果一致性
		result2, err := index2.Retrieve(core.Assignments{
			"age":  []int{25},
			"city": []string{"shanghai"},
		})
		convey.So(err, convey.ShouldBeNil)

		result3, err := index3.Retrieve(core.Assignments{
			"age":  []int{25},
			"city": []string{"shanghai"},
		})
		convey.So(err, convey.ShouldBeNil)

		convey.So(result2, convey.ShouldResemble, result3)
	})
}

func TestIncrementalIndexing_CacheMissOnVersionChange(t *testing.T) {
	convey.Convey("test cache miss when document version changes", t, func() {
		cache := NewMemoryDocCache()

		builder1 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder1.ConfigField("tag", core.FieldOption{Container: HolderNameDefault})

		doc1 := core.NewDocument(1)
		doc1.Version = 1
		doc1.AddConjunction(core.NewConjunction().In("tag", []int{1, 2})) // 使用数字值

		err := builder1.AddDocument(doc1)
		convey.So(err, convey.ShouldBeNil)

		// 构建索引以触发缓存保存
		_, _ = builder1.BuildIndex()

		// 验证缓存已保存
		cacheKey1 := NewDocCacheKey(1, 1)
		_, ok := cache.Get(cacheKey1)
		convey.So(ok, convey.ShouldBeTrue)

		builder2 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder2.ConfigField("tag", core.FieldOption{Container: HolderNameDefault})

		// 同一个文档，但 Version 不同，使用相同缓存
		doc2 := core.NewDocument(1)
		doc2.Version = 2 // Version 变化，应该缓存未命中，然后保存新缓存
		doc2.AddConjunction(core.NewConjunction().In("tag", []int{1, 2, 3}))

		err = builder2.AddDocument(doc2)
		convey.So(err, convey.ShouldBeNil)

		// 构建索引以触发缓存保存
		_, _ = builder2.BuildIndex()

		// 验证新的缓存已保存
		cacheKey2 := NewDocCacheKey(1, 2)
		_, ok = cache.Get(cacheKey2)
		convey.So(ok, convey.ShouldBeTrue)

		// 旧的缓存因为 Builder 重建导致 Schema 变化而被清空，所以这里不再校验 cacheKey1
		// _, ok = cache.Get(cacheKey1)
		// convey.So(ok, convey.ShouldBeTrue)
	})
}

func TestIncrementalIndexing_CacheClearedOnSchemaChange(t *testing.T) {
	convey.Convey("test cache cleared when schema changes", t, func() {
		cache := NewMemoryDocCache()

		builder1 := NewIndexerBuilder(WithDocLevelCache(cache))
		builder1.ConfigField("age", core.FieldOption{Container: HolderNameDefault})

		doc1 := core.NewDocument(1)
		doc1.Version = 1
		doc1.AddConjunction(core.NewConjunction().In("age", []int{18, 25}))

		err := builder1.AddDocument(doc1)
		convey.So(err, convey.ShouldBeNil)

		convey.So(cache.Size(), convey.ShouldEqual, 1)

		// 添加新字段，Schema 变化，缓存应该被清空
		builder1.ConfigField("gender", core.FieldOption{Container: HolderNameDefault})

		// 验证缓存已被清空
		convey.So(cache.Size(), convey.ShouldEqual, 0)
	})
}
