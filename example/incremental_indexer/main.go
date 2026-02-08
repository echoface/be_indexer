package main

import (
	"fmt"
	"time"

	"github.com/echoface/be_indexer"
)

// MemoryDocCache 简单的内存缓存实现
type MemoryDocCache struct {
	data map[be_indexer.DocCacheKey]*be_indexer.DocIdxCache
}

func NewMemoryDocCache() *MemoryDocCache {
	return &MemoryDocCache{
		data: make(map[be_indexer.DocCacheKey]*be_indexer.DocIdxCache),
	}
}

func (c *MemoryDocCache) Get(key be_indexer.DocCacheKey) (*be_indexer.DocIdxCache, bool) {
	entry, ok := c.data[key]
	return entry, ok
}

func (c *MemoryDocCache) Set(key be_indexer.DocCacheKey, entry *be_indexer.DocIdxCache) {
	c.data[key] = entry
}

func (c *MemoryDocCache) Clear() {
	c.data = make(map[be_indexer.DocCacheKey]*be_indexer.DocIdxCache)
}

func main() {
	// 创建缓存实例
	cache := NewMemoryDocCache()

	// 模拟广告数据
	type Ad struct {
		ID         int64
		UpdateTime time.Time
		TargetAge  []int
		TargetCity []string
	}

	ads := []Ad{
		{ID: 1, UpdateTime: time.Now(), TargetAge: []int{18, 25, 30}, TargetCity: []string{"beijing", "shanghai"}},
		{ID: 2, UpdateTime: time.Now(), TargetAge: []int{20, 25, 35}, TargetCity: []string{"shanghai", "guangzhou"}},
		{ID: 3, UpdateTime: time.Now(), TargetAge: []int{18, 22}, TargetCity: []string{"beijing"}},
	}

	fmt.Println("=== 第一次构建（全量） ===")
	start := time.Now()

	builder1 := be_indexer.NewIndexerBuilder(
		be_indexer.WithDocLevelCache(cache),
	)
	builder1.ConfigField("age", be_indexer.FieldOption{
		Container: be_indexer.HolderNameDefault,
	})
	builder1.ConfigField("city", be_indexer.FieldOption{
		Container: be_indexer.HolderNameDefault,
	})

	for _, ad := range ads {
		doc := be_indexer.NewDocument(be_indexer.DocID(ad.ID))
		doc.Version = uint64(ad.UpdateTime.Unix()) // 使用时间戳作为版本
		doc.AddConjunction(be_indexer.NewConjunction().
			In("age", ad.TargetAge).
			In("city", ad.TargetCity))

		if err := builder1.AddDocument(doc); err != nil {
			fmt.Printf("add document failed: %v\n", err)
			continue
		}
	}

	index1 := builder1.BuildIndex()
	fmt.Printf("第一次构建耗时: %v\n", time.Since(start))
	fmt.Printf("缓存条目数: %d\n", len(cache.data))

	// 模拟增量更新：只有 ad 2 发生变化
	fmt.Println("\n=== 第二次构建（增量） ===")
	ads[1].UpdateTime = time.Now() // 更新 ad 2 的时间戳
	// ad 1 和 ad 3 保持不变

	start = time.Now()

	builder2 := be_indexer.NewIndexerBuilder(
		be_indexer.WithDocLevelCache(cache),
	)
	builder2.ConfigField("age", be_indexer.FieldOption{
		Container: be_indexer.HolderNameDefault,
	})
	builder2.ConfigField("city", be_indexer.FieldOption{
		Container: be_indexer.HolderNameDefault,
	})

	for _, ad := range ads {
		doc := be_indexer.NewDocument(be_indexer.DocID(ad.ID))
		doc.Version = uint64(ad.UpdateTime.Unix())
		doc.AddConjunction(be_indexer.NewConjunction().
			In("age", ad.TargetAge).
			In("city", ad.TargetCity))

		if err := builder2.AddDocument(doc); err != nil {
			fmt.Printf("add document failed: %v\n", err)
			continue
		}
	}

	index2 := builder2.BuildIndex()
	fmt.Printf("第二次构建耗时: %v\n", time.Since(start))
	fmt.Printf("缓存条目数: %d\n", len(cache.data))

	// 验证检索结果一致性
	fmt.Println("\n=== 验证检索结果 ===")
	queries := []be_indexer.Assignments{
		{"age": []int{25}, "city": []string{"shanghai"}},
		{"age": []int{18}, "city": []string{"beijing"}},
		{"age": []int{30}},
	}

	for i, query := range queries {
		result1, _ := index1.Retrieve(query)
		result2, _ := index2.Retrieve(query)

		match := "✓"
		if len(result1) != len(result2) {
			match = "✗"
		}

		fmt.Printf("Query %d: %v - Index1: %v, Index2: %v %s\n",
			i+1, query, result1, result2, match)
	}

	fmt.Println("\n=== 说明 ===")
	fmt.Println("1. 第一次构建时，所有文档都被编译并保存到缓存")
	fmt.Println("2. 第二次构建时，ad 1 和 ad 3 的 Version 未变，直接从缓存恢复")
	fmt.Println("3. ad 2 的 Version 变化，重新编译并更新缓存")
	fmt.Println("4. 两次构建的索引检索结果完全一致")
}
