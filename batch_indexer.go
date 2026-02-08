package be_indexer

import (
	"hash"
	"hash/fnv"
	"sync"
)

// ============================================================================
// 值到 ID 的缓存优化 - 避免重复解析同一个值
// ============================================================================

// valueCache 用于缓存值的解析结果，避免重复计算
type valueCache struct {
	sync.RWMutex
	strCache map[string]uint64 // 字符串 -> ID 映射
	fnvPool  *sync.Pool      // FNV hasher 复用
}

// newValueCache 创建一个值缓存
func newValueCache() *valueCache {
	return &valueCache{
		strCache: make(map[string]uint64),
		fnvPool: &sync.Pool{
			New: func() interface{} {
				return fnv.New64()
			},
		},
	}
}

// getFNV 获取复用的 FNV hasher
func (c *valueCache) getFNV() hash.Hash64 {
	return c.fnvPool.Get().(hash.Hash64)
}

// putFNV 归还 FNV hasher
func (c *valueCache) putFNV(h hash.Hash64) {
	h.Reset()
	c.fnvPool.Put(h)
}

// fnvHashString 使用 FNV hash 计算字符串的 ID
func (c *valueCache) fnvHashString(s string) uint64 {
	h := c.getFNV()
	defer c.putFNV(h)
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// GetStrID 获取字符串的 ID（带缓存）
func (c *valueCache) GetStrID(s string) uint64 {
	c.RLock()
	if id, ok := c.strCache[s]; ok {
		c.RUnlock()
		return id
	}
	c.RUnlock()

	// 双检查锁
	c.Lock()
	defer c.Unlock()
	if id, ok := c.strCache[s]; ok {
		return id
	}
	id := c.fnvHashString(s)
	c.strCache[s] = id
	return id
}

// GetStrIDs 批量获取字符串的 ID（带缓存）
func (c *valueCache) GetStrIDs(strs []string) []uint64 {
	if len(strs) == 0 {
		return nil
	}
	result := make([]uint64, 0, len(strs))
	for _, s := range strs {
		result = append(result, c.GetStrID(s))
	}
	return result
}

// ============================================================================
// Document 批量构建优化
// ============================================================================

// BatchDocBuilder 批量构建 Document
type BatchDocBuilder struct {
	cache    *valueCache
	builders []*batchDocItem
}

type batchDocItem struct {
	id   DocID
	cons []*Conjunction
}

// NewBatchDocBuilder 创建一个批量 Document 构建器
func NewBatchDocBuilder() *BatchDocBuilder {
	return &BatchDocBuilder{
		cache: newValueCache(),
	}
}

// Add 添加一个文档
func (b *BatchDocBuilder) Add(id DocID, configureFunc func(cb *conjBuilder)) *BatchDocBuilder {
	cb := ConjBuilder()
	configureFunc(cb)
	cons := []*Conjunction{cb.Build()}
	b.builders = append(b.builders, &batchDocItem{
		id:   id,
		cons: cons,
	})
	return b
}

// AddWithCons 添加一个带 Conjunction 的文档
func (b *BatchDocBuilder) AddWithCons(id DocID, cons ...*Conjunction) *BatchDocBuilder {
	b.builders = append(b.builders, &batchDocItem{
		id:   id,
		cons: cons,
	})
	return b
}

// Build 批量构建所有 Document
func (b *BatchDocBuilder) Build() []*Document {
	docs := make([]*Document, 0, len(b.builders))
	for _, item := range b.builders {
		doc := NewDoc(item.id)
		doc.Cons = append(doc.Cons, item.cons...)
		docs = append(docs, doc)
	}
	return docs
}

// BuildTo 批量构建到提供的 slice
func (b *BatchDocBuilder) BuildTo(docs []*Document) {
	for i, item := range b.builders {
		if i >= len(docs) {
			break
		}
		docs[i].ID = item.id
		docs[i].Cons = append(docs[i].Cons[:0], item.cons...)
	}
}

// Reset 重置构建器
func (b *BatchDocBuilder) Reset() {
	b.builders = b.builders[:0]
	b.cache = newValueCache()
}

// Len 返回文档数量
func (b *BatchDocBuilder) Len() int {
	return len(b.builders)
}

// ============================================================================
// 预分配的批量索引构建
// ============================================================================

// BatchIndexer 批量索引构建器
type BatchIndexer struct {
	builder *IndexerBuilder
	cache   *valueCache
	docs    []*Document
}

// NewBatchIndexer 创建一个批量索引构建器
func NewBatchIndexer(opts ...BuilderOpt) *BatchIndexer {
	opts = append(opts, WithCacheProvider(NewMemCacheProvider()))
	return &BatchIndexer{
		builder: NewIndexerBuilder(opts...),
		cache:   newValueCache(),
		docs:    make([]*Document, 0, 1024),
	}
}

// ConfigField 配置字段
func (b *BatchIndexer) ConfigField(field string, opt FieldOption) {
	b.builder.ConfigField(BEField(field), opt)
}

// Add 添加文档
func (b *BatchIndexer) Add(id DocID, configureFunc func(cb *conjBuilder)) *BatchIndexer {
	cb := ConjBuilder()
	configureFunc(cb)
	doc := NewDoc(id)
	doc.Cons = append(doc.Cons, cb.Build())
	b.docs = append(b.docs, doc)
	return b
}

// AddDocument 直接添加 Document
func (b *BatchIndexer) AddDocument(doc *Document) *BatchIndexer {
	b.docs = append(b.docs, doc)
	return b
}

// Build 构建索引
func (b *BatchIndexer) Build() BEIndex {
	if err := b.builder.AddDocument(b.docs...); err != nil {
		panic(err)
	}
	return b.builder.BuildIndex()
}

// Reset 重置构建器
func (b *BatchIndexer) Reset() {
	b.docs = b.docs[:0]
	b.cache = newValueCache()
	b.builder.Reset()
}

// Len 返回文档数量
func (b *BatchIndexer) Len() int {
	return len(b.docs)
}

// ============================================================================
// 内存缓存提供者 - 用于 IndexerBuilder
// ============================================================================

// memCache 内存缓存实现
type memCache struct {
	sync.RWMutex
	data map[ConjID][]byte
}

func NewMemCacheProvider() CacheProvider {
	return &memCache{
		data: make(map[ConjID][]byte),
	}
}

func (c *memCache) Reset() {
	c.Lock()
	defer c.Unlock()
	c.data = make(map[ConjID][]byte)
}

func (c *memCache) Get(conjID ConjID) ([]byte, bool) {
	c.RLock()
	defer c.RUnlock()
	v, ok := c.data[conjID]
	return v, ok
}

func (c *memCache) Set(conjID ConjID, data []byte) {
	c.Lock()
	defer c.Unlock()
	c.data[conjID] = data
}
