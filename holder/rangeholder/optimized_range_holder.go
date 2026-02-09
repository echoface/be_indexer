package rangeholder

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	. "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

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
	sort.Slice(cc.values, func(i, j int) bool {
		return cc.values[i] < cc.values[j]
	})

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
	entries Entries

	// 子节点（延迟创建）
	left, right *SegmentTreeNode

	// 是否叶子节点
	isLeaf bool
}

// OptimizedRangeHolder 使用线段树 + 坐标压缩优化的 RangeHolder
type OptimizedRangeHolder struct {
	RangeHolderOption

	debug bool

	// 坐标压缩器
	compressor *CoordinateCompressor

	// 线段树根节点
	root *SegmentTreeNode

	// 待插入的范围列表（构建前收集）
	pendingRanges []pendingRange

	// 构建标记
	built bool

	// 统计
	stats HolderStats
}

// pendingRange 待处理的范围
type pendingRange struct {
	left, right int64
	eid         EntryID
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
	Operator ValueOpt `json:"operator"`
	Range    *Range   `json:"range,omitempty"`
	EqValues []int64  `json:"eq_values,omitempty"`
}

func init() {
	// 注册新的优化版本
	RegisterEntriesHolder("optimized_range", func() EntriesHolder {
		return NewOptimizedRangeHolder()
	})
}

// NewOptimizedRangeHolder 创建优化版 RangeHolder
func NewOptimizedRangeHolder(fns ...RangeOptionFn) *OptimizedRangeHolder {
	opt := NewRangeHolderOption()
	for _, fn := range fns {
		fn(opt)
	}

	return &OptimizedRangeHolder{
		RangeHolderOption: *opt,
		compressor:        NewCoordinateCompressor(),
		pendingRanges:     make([]pendingRange, 0, 1024),
	}
}

func (h *OptimizedRangeHolder) EnableDebug(debug bool) {
	h.debug = debug
}

// DumpInfo 输出统计信息
func (h *OptimizedRangeHolder) DumpInfo(buffer *strings.Builder) {
	summary := map[string]interface{}{
		"name":                    "OptimizedRangeHolder",
		"original_range_count":    h.stats.OriginalRangeCount,
		"compressed_coordinate":   h.stats.CompressedSize,
		"tree_node_count":         h.stats.TreeNodeCount,
		"memory_saved_percent":    fmt.Sprintf("%.1f%%", h.stats.MemorySavedPercent),
		"enable_float_to_int":     h.EnableFloat2Int,
		"range_convert_threshold": h.RangeCvtValuesSize,
	}
	buffer.WriteString(util.JSONPretty(summary))
}

// DumpEntries 输出 entries 详情
func (h *OptimizedRangeHolder) DumpEntries(buffer *strings.Builder) {
	buffer.WriteString("OptimizedRangeHolder entries:\n")
	buffer.WriteString(fmt.Sprintf("Compressed coordinates: %v\n", h.compressor.values))
	buffer.WriteString(h.dumpTree(h.root, 0))
}

func (h *OptimizedRangeHolder) dumpTree(node *SegmentTreeNode, depth int) string {
	if node == nil {
		return ""
	}

	indent := strings.Repeat("  ", depth)
	var sb strings.Builder

	// 显示当前节点区间
	coordRange := fmt.Sprintf("[%d,%d]", node.l, node.r)
	if h.compressor.built && node.l < len(h.compressor.values) && node.r < len(h.compressor.values) {
		actualRange := fmt.Sprintf("[%d,%d)", h.compressor.values[node.l], h.compressor.values[node.r])
		coordRange = fmt.Sprintf("%s->%s", coordRange, actualRange)
	}

	sb.WriteString(fmt.Sprintf("%sNode %s:\n", indent, coordRange))

	if len(node.entries) > 0 {
		sb.WriteString(fmt.Sprintf("%s  Entries: %v\n", indent, node.entries))
	}

	if node.left != nil {
		sb.WriteString(h.dumpTree(node.left, depth+1))
	}
	if node.right != nil {
		sb.WriteString(h.dumpTree(node.right, depth+1))
	}

	return sb.String()
}

// BuildFieldIndexingData 构建索引数据
func (h *OptimizedRangeHolder) BuildFieldIndexingData(field *FieldDesc, values *BoolValues) (IndexingData, error) {
	switch values.Operator {
	case ValueOptEQ:
		var ids []int64
		var err error
		if ids, err = parser.ParseIntegers(values.Value, h.EnableFloat2Int); err != nil {
			return nil, fmt.Errorf("field:%s value:%+v parse fail, err:%v", field.Field, values, err)
		}
		return &OptimizedRangeTxData{
			Operator: ValueOptEQ,
			EqValues: ids,
		}, nil

	case ValueOptLT, ValueOptGT, ValueOptBetween:
		rg, err := ParseRange(values.Operator, values.Value, h.EnableFloat2Int)
		if err != nil {
			return nil, err
		}

		// 优化：小范围展开为 EQ
		if rg.Size() < h.RangeCvtValuesSize {
			return &OptimizedRangeTxData{
				Operator: ValueOptEQ,
				EqValues: rg.ToSlice(),
			}, nil
		}

		// 收集坐标用于压缩
		h.compressor.AddValue(rg.left)
		h.compressor.AddValue(rg.right)

		return &OptimizedRangeTxData{
			Operator: ValueOptBetween,
			Range:    rg,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported operator:%d", values.Operator)
	}
}

// CommitFieldIndexingData 提交索引数据
func (h *OptimizedRangeHolder) CommitFieldIndexingData(tx FieldIndexingData) error {
	if tx.Data == nil {
		return nil
	}

	data := tx.Data.(*OptimizedRangeTxData)

	switch data.Operator {
	case ValueOptEQ:
		// 小范围直接记录，不经过线段树
		values := util.DistinctInteger(data.EqValues)
		for _, id := range values {
			h.pendingRanges = append(h.pendingRanges, pendingRange{
				left:  id,
				right: id,
				eid:   tx.EID,
			})
			h.compressor.AddValue(id)
		}

	case ValueOptBetween:
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
func (h *OptimizedRangeHolder) CompileEntries() error {
	// 步骤1：构建坐标压缩
	h.compressor.Build()
	h.stats.CompressedSize = h.compressor.Size()

	LogInfoIf(h.debug, "坐标压缩完成: %d -> %d (%.1fx 压缩)",
		len(h.pendingRanges)*2, // 原始坐标数（估计）
		h.stats.CompressedSize,
		float64(len(h.pendingRanges)*2)/float64(h.stats.CompressedSize))

	// 步骤2：构建线段树
	if h.stats.CompressedSize > 0 {
		h.root = h.buildTree(0, h.stats.CompressedSize-1)
	}

	// 步骤3：插入所有范围
	for _, pr := range h.pendingRanges {
		l, r := h.compressor.FindRange(pr.left, pr.right)
		if l <= r {
			h.insert(h.root, l, r, pr.eid)
		}
	}

	// 步骤4：清理临时数据
	h.pendingRanges = nil
	h.built = true

	// 计算内存节省
	h.calculateMemoryStats()

	return nil
}

// buildTree 构建线段树（动态创建节点）
func (h *OptimizedRangeHolder) buildTree(l, r int) *SegmentTreeNode {
	node := &SegmentTreeNode{
		l:       l,
		r:       r,
		entries: make([]EntryID, 0),
		isLeaf:  (l == r),
	}
	h.stats.TreeNodeCount++
	return node
}

// insert 插入范围到线段树
func (h *OptimizedRangeHolder) insert(node *SegmentTreeNode, l, r int, eid EntryID) {
	// 当前节点区间完全包含于插入区间
	if l <= node.l && node.r <= r {
		node.entries = append(node.entries, eid)
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

// GetEntries 查询 entries（核心方法）
func (h *OptimizedRangeHolder) GetEntries(field *FieldDesc, assigns Values) (EntriesCursors, error) {
	if !h.built {
		return nil, fmt.Errorf("holder not compiled")
	}

	// 解析查询值
	ids, err := parser.ParseIntegers(assigns, h.EnableFloat2Int)
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, nil
	}

	// 使用 map 去重
	resultMap := make(map[EntryID]struct{})

	for _, id := range ids {
		// 坐标压缩
		idx, ok := h.compressor.GetIdx(id)
		if !ok {
			// 值不存在，查找覆盖该值的范围
			idx = h.compressor.FindIdx(id)
			if idx >= h.stats.CompressedSize {
				continue
			}
		}

		// 查询线段树
		h.collectEntries(h.root, idx, resultMap)
	}

	// 转换为 Entries 并排序
	result := make(Entries, 0, len(resultMap))
	for eid := range resultMap {
		result = append(result, eid)
	}
	sort.Sort(result)

	// 创建 cursors
	if len(result) > 0 {
		cursor := NewEntriesCursor(NewQKey(field.Field, 0), result)
		return EntriesCursors{cursor}, nil
	}

	return nil, nil
}

// collectEntries 收集覆盖 idx 的所有 entries
func (h *OptimizedRangeHolder) collectEntries(node *SegmentTreeNode, idx int, result map[EntryID]struct{}) {
	if node == nil {
		return
	}

	// 当前节点的 entries 完全覆盖查询点
	for _, eid := range node.entries {
		result[eid] = struct{}{}
	}

	// 叶子节点停止
	if node.isLeaf {
		return
	}

	mid := (node.l + node.r) / 2

	// 递归查询子树
	if idx <= mid && node.left != nil {
		h.collectEntries(node.left, idx, result)
	} else if idx > mid && node.right != nil {
		h.collectEntries(node.right, idx, result)
	}
}

// calculateMemoryStats 计算内存节省
func (h *OptimizedRangeHolder) calculateMemoryStats() {
	// 原实现：每个唯一坐标一个 map entry
	// 新实现：压缩后坐标 + 线段树节点

	// 估算内存节省（简化计算）
	if h.stats.CompressedSize > 0 {
		originalSize := float64(h.stats.OriginalRangeCount * 2) // 每个范围2个边界
		compressedSize := float64(h.stats.CompressedSize)
		if originalSize > compressedSize {
			h.stats.MemorySavedPercent = (1.0 - compressedSize/originalSize) * 100
		}
	}
}

// DecodeFieldIndexingData 反序列化
func (h *OptimizedRangeHolder) DecodeFieldIndexingData(data []byte) (IndexingData, error) {
	var tx OptimizedRangeTxData
	err := json.Unmarshal(data, &tx)
	return &tx, err
}

func (txd *OptimizedRangeTxData) Encode() ([]byte, error) {
	return json.Marshal(txd)
}

// Range 辅助方法：转换为切片
func (rg *Range) ToSlice() []int64 {
	result := make([]int64, 0, int(rg.Size()))
	for i := rg.left; i < rg.right; i++ {
		result = append(result, i)
	}
	return result
}
