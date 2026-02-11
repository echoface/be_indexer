package rangeholder

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/echoface/be_indexer"
	indexstore "github.com/echoface/be_indexer/codegen/indexstore"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
	"google.golang.org/protobuf/proto"
)

type (
	RangeHolderOption struct {
		EnableFloat2Int    bool
		RangeCvtValuesSize float64
		RangeMax           int64
		RangeMin           int64
	}
	RangeOptionFn func(option *RangeHolderOption)

	OptimizedRangeBuilder struct {
		RangeHolderOption
		debug bool

		// 坐标压缩器 (Accumulate values)
		compressor *CoordinateCompressor

		// 待插入的范围列表（构建前收集）
		pendingRanges []pendingRange

		// 独立存储 EQ 值（混合存储策略）
		eqEntries map[int64]core.Entries

		// 统计
		stats HolderStats
	}

	// OptimizedRangeIndex 只读Holder
	OptimizedRangeIndex struct {
		RangeHolderOption
		debug bool

		// 坐标压缩器 (Built)
		compressor *CoordinateCompressor

		// 线段树根节点
		root *SegmentTreeNode

		// 压缩后的 EQ 数据
		eqFlat *util.FlatMap[int64]

		// 统计
		stats HolderStats
	}
)

func NewRangeHolderOption() *RangeHolderOption {
	return &RangeHolderOption{
		EnableFloat2Int:    true,
		RangeCvtValuesSize: 256,
		RangeMax:           math.MaxInt64,
		RangeMin:           math.MinInt64,
	}
}

func WithRangeHolderOption(opt *RangeHolderOption) RangeOptionFn {
	return func(option *RangeHolderOption) {
		*option = *opt
	}
}

func ParseBetween(value core.Values) (*Range, error) {
	var left, right int64
	switch v := value.(type) {
	case [2]int64:
		left, right = v[0], v[1]
	case []int64:
		if len(v) != 2 {
			return nil, fmt.Errorf("operator Between need two, input lenght:%d", len(v))
		}
		left, right = v[0], v[1]
	case string:
		if rgDesc := parser.NewRangeDesc(v); rgDesc != nil {
			left, right, _ = rgDesc.Values()
		} else {
			return nil, fmt.Errorf("not a valid range description, need x:y input:%s", v)
		}
	}
	if left > right {
		return nil, fmt.Errorf("%d > %d, bad range", left, right)
	}
	return NewRange(left, right), nil
}

func ParseRange(opt core.ValueOpt, value core.Values, enableF2I bool) (*Range, error) {
	switch opt {
	case core.ValueOptBetween:
		return ParseBetween(value)
	case core.ValueOptGT, core.ValueOptLT:
		var number int64
		var err error
		if number, err = parser.ParseIntegerNumber(value, enableF2I); err != nil {
			return nil, fmt.Errorf("lt/gt operator need interger, parse:%v err:%v", value, err)
		}
		if opt == core.ValueOptLT {
			return NewRange(math.MinInt64, number), nil
		}
		return NewRange(number+1, math.MaxInt64), nil
	default:
		break
	}
	return nil, fmt.Errorf("not supported operator:%d", opt)
}

type Range struct {
	left  int64
	right int64
}

func (rg *Range) String() string {
	if rg.IsLeftInf() && rg.IsRightInf() {
		return "(-inf,+inf)"
	} else if rg.IsRightInf() {
		return fmt.Sprintf("[%d,+inf)", rg.left)
	} else if rg.IsLeftInf() {
		return fmt.Sprintf("[-inf,%d)", rg.right)
	}
	return fmt.Sprintf("[%d,%d)", rg.left, rg.right)
}

func NewRange(l, r int64) *Range {
	if l == r {
		r++
	}
	return &Range{l, r}
}

func (rg *Range) Size() float64 {
	return float64(rg.right) - float64(rg.left)
}

func (rg *Range) ToSlice() []int64 {
	if rg.Size() <= 0 {
		return nil
	}
	vs := make([]int64, 0, int(rg.Size()))
	for i := rg.left; i < rg.right; i++ {
		vs = append(vs, i)
	}
	return vs
}

func (rg *Range) IsLeftInf() bool {
	return rg.left == math.MinInt64
}

func (rg *Range) IsRightInf() bool {
	return rg.left == math.MaxInt64
}

func (rg *Range) ContainValue(v int64) bool {
	return v >= rg.left && v < rg.right
}

// CoordinateCompressor 坐标压缩器
type CoordinateCompressor struct {
	// 所有唯一坐标值（已排序）
	values []int64

	// 值到索引的映射
	valueToIdx map[int64]int

	// 是否已构建
	built bool
}

// NewCoordinateCompressor 创建坐标压缩器
func NewCoordinateCompressor() *CoordinateCompressor {
	return &CoordinateCompressor{
		valueToIdx: make(map[int64]int),
	}
}

// AddValue 添加坐标值（收集阶段）
func (cc *CoordinateCompressor) AddValue(v int64) {
	if cc.built {
		panic("compressor already built")
	}
	if _, exists := cc.valueToIdx[v]; !exists {
		cc.valueToIdx[v] = -1 // 标记待处理
		cc.values = append(cc.values, v)
	}
}

// Build 构建压缩映射
func (cc *CoordinateCompressor) Build() {
	if cc.built {
		return
	}

	// 排序
	slices.Sort(cc.values)

	// 去重并建立映射
	unique := make([]int64, 0, len(cc.values))
	cc.valueToIdx = make(map[int64]int, len(cc.values))

	for _, v := range cc.values {
		if len(unique) == 0 || v != unique[len(unique)-1] {
			cc.valueToIdx[v] = len(unique)
			unique = append(unique, v)
		}
	}

	cc.values = unique
	cc.built = true
}

// GetIdx 获取压缩后的索引
func (cc *CoordinateCompressor) GetIdx(v int64) (int, bool) {
	idx, ok := cc.valueToIdx[v]
	return idx, ok
}

// FindIdx 二分查找最近的索引（用于范围查询）
func (cc *CoordinateCompressor) FindIdx(v int64) int {
	// 找到第一个 >= v 的索引
	idx := sort.Search(len(cc.values), func(i int) bool {
		return cc.values[i] >= v
	})
	return idx
}

// FindRange 查找值在压缩坐标中的范围
func (cc *CoordinateCompressor) FindRange(left, right int64) (l, r int) {
	l = cc.FindIdx(left)
	r = cc.FindIdx(right) - 1
	if r < l {
		r = l
	}
	return
}

// Size 返回压缩后的坐标数量
func (cc *CoordinateCompressor) Size() int {
	return len(cc.values)
}

// SegmentTreeNode 线段树节点
type SegmentTreeNode struct {
	// 节点覆盖的区间 [l, r]（压缩后的坐标）
	l, r int

	// 完全覆盖该节点的所有 entries
	bitmap *roaring64.Bitmap

	// 子节点（延迟创建）
	left, right *SegmentTreeNode

	// 是否叶子节点
	isLeaf bool
}

// pendingRange 待处理的范围
type pendingRange struct {
	left, right int64
	eid         core.EntryID
}

// HolderStats 统计信息
type HolderStats struct {
	OriginalRangeCount int // 原始范围数量
	CompressedSize     int // 压缩后坐标数量
	TreeNodeCount      int // 线段树节点数
	MemorySavedPercent float64
}

// OptimizedRangeTxData 优化后的序列化数据
type OptimizedRangeTxData struct {
	Operator core.ValueOpt `json:"operator"`
	Range    *Range        `json:"range,omitempty"`
	EqValues []int64       `json:"eq_values,omitempty"`
}

func init() {
	// 注册新的优化版本
	be_indexer.RegisterField("optimized_range", core.FieldImplementation{
		NewBuilder: func() core.FieldIndexBuilder { return NewOptimizedRangeBuilder() },
		NewIndex:   func() core.FieldIndex { return NewOptimizedRangeIndex() },
	})

	// 兼容旧的 holder 名称（ext_range）。
	be_indexer.RegisterField(core.HolderNameExtendRange, core.FieldImplementation{
		NewBuilder: func() core.FieldIndexBuilder { return NewOptimizedRangeBuilder() },
		NewIndex:   func() core.FieldIndex { return NewOptimizedRangeIndex() },
	})
}

func NewOptimizedRangeBuilder(fns ...RangeOptionFn) *OptimizedRangeBuilder {
	opt := NewRangeHolderOption()
	for _, fn := range fns {
		fn(opt)
	}
	return &OptimizedRangeBuilder{
		RangeHolderOption: *opt,
		compressor:        NewCoordinateCompressor(),
		pendingRanges:     make([]pendingRange, 0, 1024),
		eqEntries:         make(map[int64]core.Entries),
	}
}

func NewOptimizedRangeIndex(fns ...RangeOptionFn) *OptimizedRangeIndex {
	opt := NewRangeHolderOption()
	for _, fn := range fns {
		fn(opt)
	}
	return &OptimizedRangeIndex{
		RangeHolderOption: *opt,
		compressor:        NewCoordinateCompressor(),
	}
}

func (h *OptimizedRangeBuilder) EnableDebug(debug bool) {
	h.debug = debug
}

// BuildFieldIndexingData 构建索引数据
func (h *OptimizedRangeBuilder) BuildFieldIndexingData(field *core.FieldDesc, values *core.ValueExpr) (core.IndexingData, error) {
	switch values.Operator {
	case core.ValueOptEQ:
		var ids []int64
		var err error
		if ids, err = parser.ParseIntegers(values.Value, h.EnableFloat2Int); err != nil {
			return nil, fmt.Errorf("field:%s value:%+v parse fail, err:%v", field.Field, values, err)
		}
		return &OptimizedRangeTxData{
			Operator: core.ValueOptEQ,
			EqValues: ids,
		}, nil

	case core.ValueOptLT, core.ValueOptGT, core.ValueOptBetween:
		rg, err := ParseRange(values.Operator, values.Value, h.EnableFloat2Int)
		if err != nil {
			return nil, err
		}

		// 优化：小范围展开为 EQ
		if rg.Size() < h.RangeCvtValuesSize {
			return &OptimizedRangeTxData{
				Operator: core.ValueOptEQ,
				EqValues: rg.ToSlice(),
			}, nil
		}

		// 收集坐标用于压缩
		h.compressor.AddValue(rg.left)
		h.compressor.AddValue(rg.right)

		return &OptimizedRangeTxData{
			Operator: core.ValueOptBetween,
			Range:    rg,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported operator:%d", values.Operator)
	}
}

func (h *OptimizedRangeBuilder) DecodeFieldIndexingData(data []byte) (core.IndexingData, error) {
	var tx OptimizedRangeTxData
	err := json.Unmarshal(data, &tx)
	return &tx, err
}

// CommitFieldIndexingData 提交索引数据
func (h *OptimizedRangeBuilder) CommitFieldIndexingData(tx core.FieldIndexingData) error {
	if tx.Data == nil {
		return nil
	}

	data := tx.Data.(*OptimizedRangeTxData)

	switch data.Operator {
	case core.ValueOptEQ:
		// 小范围直接记录，不经过线段树，直接存入 eqEntries
		values := util.DistinctInteger(data.EqValues)
		for _, id := range values {
			h.eqEntries[id] = append(h.eqEntries[id], tx.EID)
		}

	case core.ValueOptBetween:
		// 大范围使用线段树
		h.pendingRanges = append(h.pendingRanges, pendingRange{
			left:  data.Range.left,
			right: data.Range.right,
			eid:   tx.EID,
		})
		h.stats.OriginalRangeCount++
	}

	return nil
}

// CompileEntries 编译索引
func (h *OptimizedRangeBuilder) CompileEntries() (core.FieldIndex, error) {
	holder := &OptimizedRangeIndex{
		RangeHolderOption: h.RangeHolderOption,
		debug:             h.debug,
		compressor:        h.compressor,
		stats:             h.stats,
	}

	// 步骤0：处理 eqEntries
	if len(h.eqEntries) > 0 {
		holder.eqFlat = util.BuildFlatMap(
			h.eqEntries,
			func(i, j int64) bool { return i < j },
			util.DefaultBlockSize,
		)
		// Clear map to save memory
		h.eqEntries = nil
	}

	// Build tree structure
	if err := holder.buildRangeIndex(h.pendingRanges); err != nil {
		return nil, err
	}

	// Reset builder
	h.pendingRanges = nil
	h.eqEntries = nil
	h.compressor = NewCoordinateCompressor()

	return holder, nil
}

// buildRangeIndex internal helper to build tree from pending ranges
func (h *OptimizedRangeIndex) buildRangeIndex(pendingRanges []pendingRange) error {
	// 步骤1：构建坐标压缩
	h.compressor.Build()
	h.stats.CompressedSize = h.compressor.Size()

	be_indexer.LogInfoIf(h.debug, "坐标压缩完成: %d -> %d (%.1fx 压缩)",
		len(pendingRanges)*2, // 原始坐标数（估计）
		h.stats.CompressedSize,
		float64(len(pendingRanges)*2)/float64(h.stats.CompressedSize))

	// 步骤2：构建线段树
	if h.stats.CompressedSize > 0 {
		h.root = h.buildTree(0, h.stats.CompressedSize-1)
	}

	// 步骤3：插入所有范围
	for _, pr := range pendingRanges {
		l, r := h.compressor.FindRange(pr.left, pr.right)
		if l <= r {
			h.insert(h.root, l, r, pr.eid)
		}
	}

	// 计算内存节省
	h.calculateMemoryStats(len(pendingRanges))

	return nil
}

// ------------------------------------------------------------------------------------------------
// OptimizedRangeIndex Implementation
// ------------------------------------------------------------------------------------------------

// DumpInfo 输出统计信息
func (h *OptimizedRangeIndex) DumpInfo(buffer *strings.Builder) {
	summary := map[string]any{
		"name":                    "OptimizedRangeIndex",
		"original_range_count":    h.stats.OriginalRangeCount,
		"compressed_coordinate":   h.stats.CompressedSize,
		"tree_node_count":         h.stats.TreeNodeCount,
		"memory_saved_percent":    fmt.Sprintf("%.1f%%", h.stats.MemorySavedPercent),
		"enable_float_to_int":     h.EnableFloat2Int,
		"range_convert_threshold": h.RangeCvtValuesSize,
	}
	if h.eqFlat != nil && len(h.eqFlat.Keys) > 0 {
		summary["eq_term_count"] = len(h.eqFlat.Keys)
		summary["eq_compressed_bytes"] = len(h.eqFlat.Data)
	}
	buffer.WriteString(util.JSONPretty(summary))
}

// buildTree 构建线段树（动态创建节点）
func (h *OptimizedRangeIndex) buildTree(l, r int) *SegmentTreeNode {
	node := &SegmentTreeNode{
		l:      l,
		r:      r,
		bitmap: roaring64.New(),
		isLeaf: (l == r),
	}
	h.stats.TreeNodeCount++
	return node
}

// insert 插入范围到线段树
func (h *OptimizedRangeIndex) insert(node *SegmentTreeNode, l, r int, eid core.EntryID) {
	// 当前节点区间完全包含于插入区间
	if l <= node.l && node.r <= r {
		node.bitmap.Add(uint64(eid))
		return
	}

	mid := (node.l + node.r) / 2

	// 左子树
	if l <= mid {
		if node.left == nil {
			node.left = h.buildTree(node.l, mid)
		}
		h.insert(node.left, l, r, eid)
	}

	// 右子树
	if r > mid {
		if node.right == nil {
			node.right = h.buildTree(mid+1, node.r)
		}
		h.insert(node.right, l, r, eid)
	}
}

func (h *OptimizedRangeIndex) findEqEntry(key int64) int {
	if h.eqFlat == nil {
		return -1
	}
	idx := sort.Search(len(h.eqFlat.Keys), func(i int) bool {
		return h.eqFlat.Keys[i].Key >= key
	})
	if idx < len(h.eqFlat.Keys) && h.eqFlat.Keys[idx].Key == key {
		return idx
	}
	return -1
}

// GetEntries 查询 entries（核心方法）
func (h *OptimizedRangeIndex) GetEntries(field *core.FieldDesc, assigns core.Values) ([]core.PostingIterator, error) {
	// 解析查询值
	ids, err := parser.ParseIntegers(assigns, h.EnableFloat2Int)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// 使用 Bitmap 去重和聚合
	resultBitmap := roaring64.New()

	for _, id := range ids {
		// 1. 查混合存储的 EQ 值
		if h.eqFlat != nil && len(h.eqFlat.Keys) > 0 {
			// 压缩模式查询
			idx := h.findEqEntry(id)
			if idx != -1 {
				eqEntry := h.eqFlat.Keys[idx]
				headerStart := int(eqEntry.HeaderIndex)
				headerEnd := len(h.eqFlat.Headers)
				if idx+1 < len(h.eqFlat.Keys) {
					headerEnd = int(h.eqFlat.Keys[idx+1].HeaderIndex)
				}

				cursor := be_indexer.NewBlockIterator(
					be_indexer.NewTerm(field.Field, id),
					h.eqFlat.Headers,
					h.eqFlat.Data,
					headerEnd,
				)
				if headerStart < headerEnd {
					cursor.LoadBlock(headerStart)
				}

				eid := cursor.Current()
				for !eid.IsNULLEntry() {
					resultBitmap.Add(uint64(eid))
					eid = cursor.SkipTo(eid + 1)
				}
			}
		}

		// 2. 查线段树（如果有）
		if h.stats.CompressedSize > 0 {
			// 坐标压缩
			idx, ok := h.compressor.GetIdx(id)
			if !ok {
				// 值不存在，查找覆盖该值的范围
				// FindIdx 返回第一个 >= id 的索引
				// 如果 id 落在区间 [v[i], v[i+1]) 中，FindIdx 会返回 i+1
				// 我们需要查询的区间索引是 i
				idx = h.compressor.FindIdx(id)

				if idx == 0 {
					// id 比所有已知坐标都小，且不等于最小值（否则 ok 为 true）
					continue
				}
				idx--
			}

			// 查询线段树
			h.collectEntries(h.root, idx, resultBitmap)
		}
	}

	if resultBitmap.IsEmpty() {
		return nil, nil
	}

	// 转换为 Entries
	vals := resultBitmap.ToArray()
	result := make(core.Entries, len(vals))
	for i, v := range vals {
		result[i] = core.EntryID(v)
	}

	// 创建 cursors
	if len(result) > 0 {
		cursor := be_indexer.NewSliceIterator(be_indexer.NewTerm(field.Field, 0), result)
		return []core.PostingIterator{cursor}, nil
	}

	return nil, nil
}

// collectEntries 收集覆盖 idx 的所有 entries
func (h *OptimizedRangeIndex) collectEntries(node *SegmentTreeNode, idx int, result *roaring64.Bitmap) {
	if node == nil {
		return
	}

	// 当前节点的 entries 完全覆盖查询点
	if !node.bitmap.IsEmpty() {
		result.Or(node.bitmap)
	}

	// 叶子节点停止
	if node.isLeaf {
		return
	}

	mid := (node.l + node.r) / 2

	// 递归查询子树
	if idx <= mid {
		h.collectEntries(node.left, idx, result)
	} else {
		h.collectEntries(node.right, idx, result)
	}
}

// calculateMemoryStats 计算内存节省
func (h *OptimizedRangeIndex) calculateMemoryStats(originalRangeCount int) {
	// 原实现：每个唯一坐标一个 map entry
	// 新实现：压缩后坐标 + 线段树节点

	// 估算内存节省（简化计算）
	if h.stats.CompressedSize > 0 {
		originalSize := float64(originalRangeCount * 2) // 每个范围2个边界
		compressedSize := float64(h.stats.CompressedSize)
		if originalSize > compressedSize {
			h.stats.MemorySavedPercent = (1.0 - compressedSize/originalSize) * 100
		}
	}
}

func (txd *OptimizedRangeTxData) Encode() ([]byte, error) {
	return json.Marshal(txd)
}

func (h *OptimizedRangeIndex) Serialize() ([]byte, error) {
	dump := &indexstore.RangeHolderDump{}

	// 1. 序列化 EQ postings：直接写入 flat(compressed) 表示，避免解压。
	var fm *util.FlatMap[int64]
	if h.eqFlat != nil && len(h.eqFlat.Keys) > 0 {
		fm = h.eqFlat
	}

	if fm != nil && len(fm.Keys) > 0 {
		eq := &indexstore.CompressedInt64FlatMap{
			BlockSize: uint32(util.DefaultBlockSize),
			Keys:      make([]*indexstore.Int64FlatMapKey, 0, len(fm.Keys)),
			Headers:   make([]*indexstore.FlatMapHeader, 0, len(fm.Headers)),
			Data:      append([]byte(nil), fm.Data...),
		}
		for _, ke := range fm.Keys {
			eq.Keys = append(eq.Keys, &indexstore.Int64FlatMapKey{
				Key:          ke.Key,
				HeaderOffset: ke.HeaderIndex,
				Length:       ke.Length,
			})
		}
		for _, ch := range fm.Headers {
			eq.Headers = append(eq.Headers, &indexstore.FlatMapHeader{
				LastEntryId: ch.LastEntryID,
				Offset:      ch.Offset,
			})
		}
		dump.EqFlat = eq
	}

	// 2. 序列化 Ranges
	ranges, err := h.collectRangesFromTree()
	if err != nil {
		return nil, err
	}
	for _, r := range ranges {
		dump.Ranges = append(dump.Ranges, r)
	}

	return proto.Marshal(dump)
}

func (h *OptimizedRangeIndex) collectRangesFromTree() ([]*indexstore.RangeEntry, error) {
	var results []*indexstore.RangeEntry
	var err error
	var traverse func(node *SegmentTreeNode)
	traverse = func(node *SegmentTreeNode) {
		if node == nil || err != nil {
			return
		}
		if !node.bitmap.IsEmpty() {
			leftVal := h.compressor.values[node.l]
			var rightVal int64
			if node.r+1 < len(h.compressor.values) {
				rightVal = h.compressor.values[node.r+1]
			} else {
				rightVal = math.MaxInt64
			}

			bBytes, e := node.bitmap.ToBytes()
			if e != nil {
				err = e
				return
			}

			results = append(results, &indexstore.RangeEntry{
				Left:   leftVal,
				Right:  rightVal,
				Bitmap: bBytes,
			})
		}
		traverse(node.left)
		traverse(node.right)
	}
	traverse(h.root)
	return results, err
}

func (h *OptimizedRangeIndex) Deserialize(data []byte) error {
	dump := &indexstore.RangeHolderDump{}
	if err := proto.Unmarshal(data, dump); err != nil {
		return err
	}

	// Reset state
	h.root = nil
	h.eqFlat = nil
	h.compressor = NewCoordinateCompressor()
	h.stats = HolderStats{}

	// 1. 反序列化 EQ postings
	if dump.EqFlat != nil {
		fm := &util.FlatMap[int64]{}
		fm.Keys = make([]util.KeyEntry[int64], 0, len(dump.EqFlat.Keys))
		for _, k := range dump.EqFlat.Keys {
			fm.Keys = append(fm.Keys, util.KeyEntry[int64]{
				Key:         k.Key,
				HeaderIndex: k.HeaderOffset,
				Length:      k.Length,
			})
		}
		fm.Headers = make([]util.CompHeader, 0, len(dump.EqFlat.Headers))
		for _, ch := range dump.EqFlat.Headers {
			fm.Headers = append(fm.Headers, util.CompHeader{LastEntryID: ch.LastEntryId, Offset: ch.Offset})
		}
		fm.Data = append([]byte(nil), dump.EqFlat.Data...)
		h.eqFlat = fm
	}

	// Reconstruct ranges to build tree
	var pendingRanges []pendingRange

	for _, rg := range dump.Ranges {
		for _, id := range rg.Ids {
			pendingRanges = append(pendingRanges, pendingRange{
				left:  rg.Left,
				right: rg.Right,
				eid:   core.EntryID(id),
			})
		}
		if len(rg.Bitmap) > 0 {
			bm := roaring64.New()
			if err := bm.UnmarshalBinary(rg.Bitmap); err != nil {
				return err
			}
			it := bm.Iterator()
			for it.HasNext() {
				eid := it.Next()
				pendingRanges = append(pendingRanges, pendingRange{
					left:  rg.Left,
					right: rg.Right,
					eid:   core.EntryID(eid),
				})
			}
		}

		h.compressor.AddValue(rg.Left)
		h.compressor.AddValue(rg.Right)
	}

	return h.buildRangeIndex(pendingRanges)
}

