package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultEntriesHolder_CompressedMode(t *testing.T) {
	// 准备测试数据
	// core.Term 1: FieldID=1, IDValue=100 -> [EID(1), EID(3), EID(100), EID(200)...]
	// 构造一个超过 BlockSize (128) 的长链，验证跨 Block 跳跃

	count := 300
	term1 := NewKVTerm(1, "100")
	var entries1 core.Entries
	for i := 0; i < count; i++ {
		// 生成单调递增的 core.EntryID
		// core.EntryID 包含 core.DocID。简单起见，让 core.DocID = i
		eid := core.NewEntryID(core.NewConjID(core.DocID(i), 0, 1), true)
		entries1 = append(entries1, eid)
	}

	holderBuilder := NewDefaultEntriesHolder()
	// 构建阶段数据写入 pair buffer，然后 CompileEntries 转为压缩 flat
	for _, eid := range entries1 {
		holderBuilder.builder.AddEntryWithFieldID(term1.FieldID, term1.Value, eid)
	}

	// 模拟 Parser
	holderBuilder.RegisterFieldTokenizer("field1", nil) // 使用默认

	// 触发压缩编译
	h, err := holderBuilder.CompileEntries()
	assert.NoError(t, err)
	holder := h.(*CompressedKVIndex)

	// 验证状态
	assert.NotNil(t, holder.reader)
	assert.True(t, len(holder.segmentData) > 0)
	// assert.True(t, len(holder.flat.Headers) >= 3) // Hard to check internal headers count easily now

	// 验证查询
	desc1 := &core.FieldDesc{ID: 1, Field: "field1"}

	// Case 1: 查第一个元素
	cursors, err := holder.GetEntries(desc1, core.Values("100"))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(cursors))
	if len(cursors) > 0 {
		cur := cursors[0]
		// 初始状态应该指向第一个元素
		assert.Equal(t, entries1[0], cur.Current())

		// Case 2: SkipTo 同一个 Block 内的元素
		target := entries1[10] // index 10
		res := cur.SkipTo(target)
		assert.Equal(t, target, res)
		assert.Equal(t, target, cur.Current())

		// Case 3: SkipTo 下一个 Block 的元素
		// BlockSize = 128. entries1[130] 应该在第二个 Block
		target = entries1[130]
		res = cur.SkipTo(target)
		assert.Equal(t, target, res)
		assert.Equal(t, target, cur.Current())

		// Case 4: SkipTo 更远的 Block
		target = entries1[299] // 最后一个元素
		res = cur.SkipTo(target)
		assert.Equal(t, target, res)

		// Case 5: SkipTo 越界
		target = core.NewEntryID(core.NewConjID(core.DocID(count+1), 0, 1), true)
		res = cur.SkipTo(target)
		assert.True(t, res.IsNULLEntry())
		assert.True(t, cur.Current().IsNULLEntry())
	}

	// 验证 DumpInfo
	sb := &strings.Builder{}
	holder.DumpInfo(sb)
	assert.Contains(t, sb.String(), `"mode": "segment_phase2"`)
}
