package be_indexer

// DocCacheKey 文档缓存键
type DocCacheKey struct {
	DocID   DocID
	Version uint64
}

// DocIdxCache 文档级缓存条目
type DocIdxCache struct {
	DocID         DocID
	Version       uint64
	SchemaHash    uint64         // 字段配置哈希，用于校验
	ConjIdxCaches []ConjIdxCache // 每个 Conjunction 的结果
}

// ConjIdxCache Conjunction 级别缓存
type ConjIdxCache struct {
	ConjIdx       int
	ConjSize      int
	WildcardEID   EntryID        // 0 表示没有 wildcard
	FieldCacheIdx []FieldIndexes // 每个字段的 Transactions
}

// FieldIndexes 字段级别的 Transaction 缓存
type FieldIndexes struct {
	Field   BEField
	Entries []IdxCacheEntry
}

// IdxCacheEntry 单个 Transaction 缓存条目
type IdxCacheEntry struct {
	EID       EntryID
	DataBytes []byte // TxData.Encode() 序列化结果
}

// DocLevelCache 文档级缓存接口
// 由业务方实现，可以是内存缓存、Redis、文件等
type DocLevelCache interface {
	// Get 获取缓存
	Get(key DocCacheKey) (*DocIdxCache, bool)

	// Set 设置缓存
	Set(key DocCacheKey, entry *DocIdxCache)

	// Clear 清空缓存（Schema 变化时调用）
	Clear()
}

// NewDocCacheKey 创建缓存键
func NewDocCacheKey(docID DocID, version uint64) DocCacheKey {
	return DocCacheKey{
		DocID:   docID,
		Version: version,
	}
}
