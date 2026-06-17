// === 新增文件: heap_cursor.go ===
// 优化kgroups检索方法性能
package be_indexer

import "container/heap"

// FieldCursorHeap 最小堆结构
type FieldCursorHeap struct {
    cursors []*FieldCursor  // 堆数组
    indices []int           // 记录每个游标在堆中的位置（用于删除）
}

// heap.Interface 实现
func (h *FieldCursorHeap) Len() int {
    return len(h.cursors)
}

func (h *FieldCursorHeap) Less(i, j int) bool {
    return h.cursors[i].GetCurEntryID() < h.cursors[j].GetCurEntryID()
}

func (h *FieldCursorHeap) Swap(i, j int) {
    h.cursors[i], h.cursors[j] = h.cursors[j], h.cursors[i]
    h.indices[i], h.indices[j] = h.indices[j], h.indices[i]  // 更新位置记录
}

func (h *FieldCursorHeap) Push(x interface{}) {
    cursor := x.(*FieldCursor)
    h.indices = append(h.indices, len(h.cursors))  // 记录位置
    h.cursors = append(h.cursors, cursor)
}

func (h *FieldCursorHeap) Pop() interface{} {
    n := len(h.cursors)
    x := h.cursors[n-1]
    h.cursors = h.cursors[:n-1]
    h.indices = h.indices[:n-1]
    return x
}

// 堆特定方法
func (h *FieldCursorHeap) GetMin() *FieldCursor {
    if len(h.cursors) == 0 {
        return nil
    }
    return h.cursors[0]  // O(1)
}

func (h *FieldCursorHeap) GetNth(n int) *FieldCursor {
    if n >= len(h.cursors) {
        return nil
    }
    return h.cursors[n]  // O(1)
}

// PopMin 弹出最小值，并将其 SkipTo
func (h *FieldCursorHeap) PopMin(nextID EntryID) {
    if len(h.cursors) == 0 {
        return
    }
    
    cursor := h.cursors[0]
    cursor.SkipTo(nextID)  // 修改游标
    
    // 重新堆化最小值的位置
    heap.Fix(h, 0)  // O(log m)
}

// 初始化堆
func NewFieldCursorHeap(fieldCursors FieldCursors) *FieldCursorHeap {
    h := &FieldCursorHeap{
        cursors: make([]*FieldCursor, len(fieldCursors)),
        indices: make([]int, len(fieldCursors)),
    }
    
    for i := range fieldCursors {
        h.cursors[i] = &fieldCursors[i]
        h.indices[i] = i
    }
    
    heap.Init(h)  // O(m)
    return h
}