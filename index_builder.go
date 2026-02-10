package be_indexer

import (
	"fmt"
	"hash/fnv"

	"github.com/echoface/be_indexer/util"
)

type (
	IndexerBuilder struct {
		BuilderOption

		indexer BEIndex

		fieldsData map[BEField]*FieldDesc

		schemaHash uint64 // 字段配置哈希，用于校验缓存有效性
	}

	// CacheProvider a interface
	CacheProvider interface {
		// Reset expire all existing cache data
		Reset()

		Get(conjID ConjID) ([]byte, bool)

		Set(conjID ConjID, data []byte)
	}

	BuilderOption struct {
		indexerType     IndexerType
		badConjBehavior BadConjBehavior // 是否允许一个doc中部分Conjunction解析失败
		docLevelCache   DocLevelCache   // 【增量缓存】文档级缓存
	}

	BuilderOpt func(builder *IndexerBuilder)

	IndexerType int

	// 索引构建的中间结果
	ConjIndexingData struct {
		idx       int
		incSize   int
		conjID    ConjID
		fieldData []*FieldIndexingData
	}

	BadConjBehavior int
)

const (
	ErrorBadConj = 0
	SkipBadConj  = 1
	PanicBadConj = 2

	IndexerTypeDefault = IndexerType(0)
	IndexerTypeCompact = IndexerType(1)
)

func WithBadConjBehavior(v BadConjBehavior) BuilderOpt {
	return func(builder *IndexerBuilder) {
		builder.badConjBehavior = v
	}
}

// WithDocLevelCache 设置文档级缓存
func WithDocLevelCache(cache DocLevelCache) BuilderOpt {
	return func(builder *IndexerBuilder) {
		builder.docLevelCache = cache
	}
}

func WithIndexerType(t IndexerType) BuilderOpt {
	return func(builder *IndexerBuilder) {
		builder.indexerType = t
	}
}

func NewIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder {
	builder := &IndexerBuilder{
		indexer:    NewKGroupsBEIndex(),
		fieldsData: map[BEField]*FieldDesc{},
	}
	for _, optFn := range opts {
		optFn(builder)
	}
	builder.initIndexer()
	return builder
}

func NewCompactIndexerBuilder(opts ...BuilderOpt) *IndexerBuilder {
	opts = append(opts, WithIndexerType(IndexerTypeCompact))
	return NewIndexerBuilder(opts...)
}

func (b *IndexerBuilder) Reset() {
	b.initIndexer()
}

func (b *IndexerBuilder) initIndexer() {
	switch b.indexerType {
	case IndexerTypeDefault:
		b.indexer = NewKGroupsBEIndex()
	case IndexerTypeCompact:
		b.indexer = NewCompactedBEIndex()
	default:
		util.PanicIf(true, "type:%d not supported", b.indexerType)
	}
}

func (b *IndexerBuilder) ConfigField(field BEField, settings FieldOption) {
	_, err := b.configureField(field, settings)
	util.PanicIfErr(err, "config field:%s with option fail:%+v", field, settings)
}

// 从业务定义的文档构建
func (b *IndexerBuilder) AddDocument(docs ...*Document) error {
	for _, doc := range docs {
		util.PanicIf(doc == nil, "nil document not be allowed")
		if err := b.validDocument(doc); err != nil {
			return err
		}
		if err := b.buildDocEntries(doc); err != nil {
			return err
		}
	}
	return nil
}

// 从文档级索引缓存（中间结果）恢复
func (b *IndexerBuilder) AddDocIndexingData(cached *DocIdxCache) error {
	for _, conjResult := range cached.ConjIdxCaches {
		// 恢复成 ConjIndexingData
		cd, err := b.toConjIndexingData(cached.DocID, &conjResult)
		if err != nil {
			return err
		}
		// 统一提交
		b.commitConjIndexingData(cd)
	}
	return nil
}

// toConjIndexingData 将缓存结果转换为 ConjIndexingData
func (b *IndexerBuilder) toConjIndexingData(docID DocID, conjResult *ConjIdxCache) (*ConjIndexingData, error) {
	cd := &ConjIndexingData{
		idx:       conjResult.ConjIdx,
		conjID:    NewConjID(docID, conjResult.ConjIdx, conjResult.ConjSize),
		incSize:   conjResult.ConjSize,
		fieldData: make([]*FieldIndexingData, 0),
	}

	container := b.indexer.newContainer(conjResult.ConjSize)

	// 恢复每个 Field
	for _, fieldTx := range conjResult.FieldCacheIdx {
		desc := b.fieldsData[fieldTx.Field]
		if desc == nil {
			return nil, fmt.Errorf("field %s not configured", fieldTx.Field)
		}
		holder := container.CreateHolder(desc)

		// 恢复每个 Expression
		for _, txCache := range fieldTx.Entries {
			txData, err := holder.DecodeFieldIndexingData(txCache.DataBytes)
			if err != nil {
				return nil, fmt.Errorf("decode tx data failed for field %s: %v", fieldTx.Field, err)
			}

			// 构造 FieldIndexingData
			tx := &FieldIndexingData{
				field:  desc,
				holder: holder,
				EID:    txCache.EID,
				Data:   txData,
			}
			cd.fieldData = append(cd.fieldData, tx)
		}
	}

	return cd, nil
}

func (b *IndexerBuilder) BuildIndex() BEIndex {
	b.indexer.setFieldDesc(b.fieldsData)

	err := b.indexer.compileIndexer()
	util.PanicIfErr(err, "fail compile indexer data, err:%+v", err)

	return b.indexer
}

func (b *IndexerBuilder) configureField(field BEField, option FieldOption) (*FieldDesc, error) {
	if _, ok := b.fieldsData[field]; ok {
		return nil, fmt.Errorf("can't configure field:%s twice", field)
	}
	if len(option.Container) == 0 {
		option.Container = HolderNameDefault
		Logger.Infof("not configure container for field:%s, use default", field)
	}

	fieldID := uint64(len(b.fieldsData))
	desc := &FieldDesc{
		ID:          fieldID,
		Field:       field,
		FieldOption: option,
	}
	b.fieldsData[field] = desc

	// 【增量缓存】更新 schema hash 并清空缓存
	b.updateSchemaHash()
	if b.docLevelCache != nil {
		b.docLevelCache.Clear()
		Logger.Infof("schema changed, doc level cache cleared")
	}

	Logger.Infof("configure field:%s, fieldID:%d\n", field, desc.ID)
	return desc, nil
}

// updateSchemaHash 计算字段配置哈希
func (b *IndexerBuilder) updateSchemaHash() {
	h := fnv.New64a()
	for field, desc := range b.fieldsData {
		h.Write([]byte(field))
		h.Write([]byte(desc.Container))
		// 可以扩展：包含更多配置项
	}
	b.schemaHash = h.Sum64()
}

func (b *IndexerBuilder) validDocument(doc *Document) error {
	// util.PanicIf(len(doc.Cons) == 0, "no conjunctions in this document")
	// util.PanicIf(len(doc.Cons) > 0xFF, "number of conjunction need less than 256")
	if len(doc.Cons) == 0 {
		return fmt.Errorf("no conjunctions in this document")
	}
	if len(doc.Cons) > 0xFF {
		return fmt.Errorf("number of conjunction need less than 256")
	}
	return nil
}

func (b *IndexerBuilder) createFieldData(field BEField) *FieldDesc {
	if desc, hit := b.fieldsData[field]; hit {
		return desc
	}
	desc, err := b.configureField(field, FieldOption{
		Container: HolderNameDefault,
	})
	util.PanicIfErr(err, "this should not happened for default settings")
	return desc
}

// commitConjIndexingData 统一提交 Conjunction 数据
func (b *IndexerBuilder) commitConjIndexingData(cd *ConjIndexingData) {
	// 提交 Wildcard
	if cd.incSize == 0 {
		b.indexer.addWildcardEID(NewEntryID(cd.conjID, true))
	}

	// 提交所有 FieldIndexingData
	for _, tx := range cd.fieldData {
		err := tx.holder.CommitFieldIndexingData(*tx)
		util.PanicIfErr(err, "commit indexing data failed")
	}
}

func (b *IndexerBuilder) buildDocEntries(doc *Document) error {
	util.PanicIf(len(doc.Cons) == 0, "no conjunctions in this document")
	util.PanicIf(len(doc.Cons) > 0xFF, "number of conjunction need less than 256")

	// 【增量缓存】尝试从文档级缓存恢复
	if b.docLevelCache != nil && doc.Version > 0 {
		cacheKey := NewDocCacheKey(doc.ID, doc.Version)
		if cached, ok := b.docLevelCache.Get(cacheKey); ok && cached.SchemaHash == b.schemaHash {
			Logger.Debugf("doc cache hit: docID=%d, version=%d", doc.ID, doc.Version)
			return b.AddDocIndexingData(cached)
		}
	}

	// 缓存未命中，构建并捕获结果
	var cacheEntry *DocIdxCache
	if b.docLevelCache != nil && doc.Version > 0 {
		cacheEntry = &DocIdxCache{
			DocID:      doc.ID,
			Version:    doc.Version,
			SchemaHash: b.schemaHash,
		}
	}

	// 阶段 1：准备阶段 - 收集所有 Conjunction 的数据
	allConjData := make([]ConjIndexingData, 0, len(doc.Cons))

ConjLoop:
	for idx, conj := range doc.Cons {
		incSize := conj.CalcConjSize()
		conjID := NewConjID(doc.ID, idx, incSize)

		// 收集 Wildcard（暂不提交）
		if incSize == 0 {
			// Wildcard 将在阶段 2 统一提交
		}

		conjIndexingData, err := b.indexingConjunction(conjID, conj)
		if err != nil {
			switch b.badConjBehavior {
			case SkipBadConj:
				Logger.Errorf("indexing conj:%s fail:%v", conjID.String(), err)
				continue ConjLoop
			case ErrorBadConj:
				return fmt.Errorf("indexing conj:%s fail:%v", conjID.String(), err)
			default:
			}
			util.PanicIf(true, "indexing conj:%s fail:%v", conjID.String(), err)
		}

		allConjData = append(allConjData, ConjIndexingData{
			idx:       idx,
			conjID:    conjID,
			incSize:   incSize,
			fieldData: conjIndexingData,
		})
	}

	// 阶段 2：提交阶段 - 所有 Conjunction 都成功准备后，统一提交到 holder
	for i := range allConjData {
		cd := &allConjData[i]
		// 统一提交
		b.commitConjIndexingData(cd)

		// 【增量缓存】捕获 Conjunction 结果
		if cacheEntry != nil {
			cacheEntry.ConjIdxCaches = append(cacheEntry.ConjIdxCaches, cd.toCacheResult())
		}
	}

	// 【增量缓存】保存到缓存
	if cacheEntry != nil && len(cacheEntry.ConjIdxCaches) > 0 {
		cacheKey := NewDocCacheKey(doc.ID, doc.Version)
		b.docLevelCache.Set(cacheKey, cacheEntry)
		Logger.Debugf("doc cache saved: docID=%d, version=%d", doc.ID, doc.Version)
	}

	return nil
}

// indexingConjunction return (txs []*IndexingBETx, needCache bool, err error)
func (b *IndexerBuilder) indexingConjunction(conjID ConjID, conj *Conjunction) ([]*FieldIndexingData, error) {
	incSize := conjID.Size()
	conjIndexingTXs := make([]*FieldIndexingData, 0, len(conj.Expressions))

	container := b.indexer.newContainer(incSize)

	for field, exprs := range conj.Expressions {
		for _, expr := range exprs {
			desc := b.createFieldData(field)
			holder := container.CreateHolder(desc)

			var err error
			var txData IndexingData
			if txData, err = holder.BuildFieldIndexingData(desc, expr); err != nil {
				return nil, fmt.Errorf("indexing field:%s fail:%v", field, err)
			}
			entryID := NewEntryID(conjID, expr.Incl)
			tx := &FieldIndexingData{field: desc, holder: holder, EID: entryID, Data: txData}
			conjIndexingTXs = append(conjIndexingTXs, tx)
		}
	}
	return conjIndexingTXs, nil
}

// toCacheResult 转换为可缓存的格式
func (cd *ConjIndexingData) toCacheResult() ConjIdxCache {
	result := ConjIdxCache{
		ConjIdx:  cd.idx,
		ConjSize: cd.incSize,
	}

	// 捕获 Wildcard
	if cd.incSize == 0 {
		result.WildcardEID = NewEntryID(cd.conjID, true)
	}

	// 按字段分组捕获 Transactions
	fieldTxMap := make(map[BEField]*FieldIndexes)
	for _, tx := range cd.fieldData {
		field := tx.field.Field
		if _, ok := fieldTxMap[field]; !ok {
			fieldTxMap[field] = &FieldIndexes{
				Field: field,
			}
		}

		// 序列化 TxData
		dataBytes, err := tx.Data.Encode()
		if err != nil {
			Logger.Errorf("encode tx data failed for field %s: %v", field, err)
			continue
		}

		fieldTxMap[field].Entries = append(fieldTxMap[field].Entries, IdxCacheEntry{
			EID:       tx.EID,
			DataBytes: dataBytes,
		})
	}

	// 转换 map 为 slice
	for _, fieldTx := range fieldTxMap {
		if len(fieldTx.Entries) > 0 {
			result.FieldCacheIdx = append(result.FieldCacheIdx, *fieldTx)
		}
	}

	return result
}
