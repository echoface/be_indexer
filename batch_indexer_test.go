package be_indexer

import (
	"hash/fnv"
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestValueCache(t *testing.T) {
	convey.Convey("test valueCache", t, func() {
		cache := newValueCache()

		// 首次获取
		id1 := cache.GetStrID("beijing")
		convey.So(id1, convey.ShouldNotEqual, 0)

		// 再次获取应该命中缓存
		id2 := cache.GetStrID("beijing")
		convey.So(id2, convey.ShouldEqual, id1)

		// 不同字符串应该不同
		id3 := cache.GetStrID("shanghai")
		convey.So(id3, convey.ShouldNotEqual, id1)
	})
}

func TestValueCache_FNVPool(t *testing.T) {
	convey.Convey("test FNV hasher pool", t, func() {
		cache := newValueCache()

		// 并发测试
		for i := 0; i < 100; i++ {
			id := cache.GetStrID("test")
			convey.So(id, convey.ShouldNotEqual, 0)
		}
	})
}

func TestValueCache_Batch(t *testing.T) {
	convey.Convey("test batch string ID", t, func() {
		cache := newValueCache()

		strs := []string{"beijing", "shanghai", "guangzhou", "beijing", "shanghai"}
		ids := cache.GetStrIDs(strs)

		convey.So(len(ids), convey.ShouldEqual, 5)
		convey.So(ids[0], convey.ShouldEqual, ids[3]) // beijing 相同
		convey.So(ids[1], convey.ShouldEqual, ids[4]) // shanghai 相同
	})
}

func TestBatchDocBuilder(t *testing.T) {
	convey.Convey("test BatchDocBuilder", t, func() {
		builder := NewBatchDocBuilder()

		builder.Add(1, func(cb *conjBuilder) {
			cb.InInt("age", 18, 25)
			cb.InStr("city", "beijing")
		})

		builder.Add(2, func(cb *conjBuilder) {
			cb.InInt("age", 20, 30)
			cb.InStr("city", "shanghai")
		})

		docs := builder.Build()

		convey.So(len(docs), convey.ShouldEqual, 2)
		convey.So(docs[0].ID, convey.ShouldEqual, DocID(1))
		convey.So(docs[1].ID, convey.ShouldEqual, DocID(2))
	})
}

func TestBatchDocBuilder_AddWithCons(t *testing.T) {
	convey.Convey("test BatchDocBuilder AddWithCons", t, func() {
		builder := NewBatchDocBuilder()

		conj := GetConjunction()
		conj.InInt("age", 18)

		builder.AddWithCons(1, conj)

		docs := builder.Build()
		convey.So(len(docs), convey.ShouldEqual, 1)

		PutConjunction(conj)
	})
}

func TestBatchDocBuilder_Reset(t *testing.T) {
	convey.Convey("test BatchDocBuilder Reset", t, func() {
		builder := NewBatchDocBuilder()

		builder.Add(1, func(cb *conjBuilder) {
			cb.InInt("age", 18)
		})

		convey.So(builder.Len(), convey.ShouldEqual, 1)

		builder.Reset()

		convey.So(builder.Len(), convey.ShouldEqual, 0)
	})
}

func TestMemCacheProvider(t *testing.T) {
	convey.Convey("test MemCacheProvider", t, func() {
		cache := NewMemCacheProvider()

		// 设置
		cache.Set(ConjID(1), []byte("test"))

		// 获取
		data, ok := cache.Get(ConjID(1))
		convey.So(ok, convey.ShouldBeTrue)
		convey.So(string(data), convey.ShouldEqual, "test")

		// 不存在
		_, ok = cache.Get(ConjID(2))
		convey.So(ok, convey.ShouldBeFalse)

		// 重置
		cache.Reset()
		_, ok = cache.Get(ConjID(1))
		convey.So(ok, convey.ShouldBeFalse)
	})
}

func TestBatchIndexer(t *testing.T) {
	convey.Convey("test BatchIndexer", t, func() {
		indexer := NewBatchIndexer()

		// 配置字段
		indexer.ConfigField("age", FieldOption{Container: HolderNameDefault})
		indexer.ConfigField("city", FieldOption{Container: HolderNameDefault})

		// 添加文档
		indexer.Add(1, func(cb *conjBuilder) {
			cb.InInt("age", 18, 25)
			cb.InStr("city", "beijing")
		})

		indexer.Add(2, func(cb *conjBuilder) {
			cb.InInt("age", 20, 30)
			cb.InStr("city", "shanghai")
		})

		convey.So(indexer.Len(), convey.ShouldEqual, 2)

		// 构建
		idx := indexer.Build()
		convey.So(idx, convey.ShouldNotBeNil)

		// 重置
		indexer.Reset()
		convey.So(indexer.Len(), convey.ShouldEqual, 0)
	})
}

func BenchmarkValueCache(b *testing.B) {
	cache := newValueCache()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cache.GetStrID("beijing")
	}
}

func BenchmarkValueCache_NoCache(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 模拟没有缓存的情况
		h := fnv.New64()
		_, _ = h.Write([]byte("beijing"))
		_ = h.Sum64()
	}
}

func BenchmarkBatchDocBuilder(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		builder := NewBatchDocBuilder()

		builder.Add(1, func(cb *conjBuilder) {
			cb.InInt("age", 18, 25, 30)
			cb.InStr("city", "beijing", "shanghai")
			cb.NotInInt("status", 0)
		})

		docs := builder.Build()
		_ = docs
	}
}
