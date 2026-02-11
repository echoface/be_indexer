package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"fmt"
	"strings"
	"testing"

	"github.com/echoface/be_indexer/util"
)

func TestDefaultEntriesHolder_LoadData(t *testing.T) {
	// 准备测试数据
	// 初始化 Holder
	holderBuilder := NewDefaultEntriesHolder()

	// 预先在 Parser 中注册值，获取对应的内部 ID
	p := holderBuilder.GetTokenizer("field1") // DefaultEntriesHolder 默认所有字段共用一个 Parser

	ids1, _ := p.TokenizeValue(100)
	id1 := ids1[0]

	ids2, _ := p.TokenizeValue(200)
	id2 := ids2[0]

	ids3, _ := p.TokenizeValue(50)
	id3 := ids3[0]

	term1 := NewKVTerm(1, id1)
	term2 := NewKVTerm(1, id2)
	term3 := NewKVTerm(2, id3)

	entries1 := core.Entries{
		core.NewEntryID(core.NewConjID(1, 0, 1), true),
		core.NewEntryID(core.NewConjID(3, 0, 1), true),
	}
	entries2 := core.Entries{
		core.NewEntryID(core.NewConjID(2, 0, 1), true),
	}
	entries3 := core.Entries{
		core.NewEntryID(core.NewConjID(4, 0, 1), true),
		core.NewEntryID(core.NewConjID(5, 0, 1), true),
		core.NewEntryID(core.NewConjID(6, 0, 1), true),
	}

	// 重新按 ID 排序，因为分配的 ID 不一定按顺序
	// 但为了测试简单，我们假设分配顺序（通常是自增的，或者是哈希的）
	// 这里我们动态调整 terms 顺序以满足 LoadData 的要求（Term 必须有序）

	terms := []util.KeyEntry[KVTerm]{
		{Key: term1, Length: uint32(len(entries1))},
		{Key: term2, Length: uint32(len(entries2))},
		{Key: term3, Length: uint32(len(entries3))},
	}

	// 简单的冒泡排序
	for i := 0; i < len(terms); i++ {
		for j := i + 1; j < len(terms); j++ {
			shouldSwap := false
			if terms[i].Key.FieldID > terms[j].Key.FieldID {
				shouldSwap = true
			} else if terms[i].Key.FieldID == terms[j].Key.FieldID && terms[i].Key.Value > terms[j].Key.Value {
				shouldSwap = true
			}
			if shouldSwap {
				terms[i], terms[j] = terms[j], terms[i]
			}
		}
	}

	// 计算 Offset 并填充数据
	var flatEntries []core.EntryID
	var currentOffset uint32
	for i := range terms {
		terms[i].HeaderIndex = currentOffset
		currentOffset += terms[i].Length

		// 根据 core.Term 找到对应的 entries
		if terms[i].Key == term1 {
			flatEntries = append(flatEntries, entries1...)
		} else if terms[i].Key == term2 {
			flatEntries = append(flatEntries, entries2...)
		} else if terms[i].Key == term3 {
			flatEntries = append(flatEntries, entries3...)
		}
	}

	h, err := holderBuilder.LoadData(terms, flatEntries)
	if err != nil {
		t.Fatal(err)
	}
	holder := h.(*CompressedKVIndex)

	// 验证 Holder 状态
	if holderBuilder.builder != nil {
		t.Errorf("expected builder=nil after CompileEntries")
	}
	if holder.reader == nil {
		t.Errorf("expected reader not nil")
	}
	if holder.reader != nil && holder.reader.dict.Size() != len(terms) {
		t.Errorf("expected %d terms, got %d", len(terms), holder.reader.dict.Size())
	}
	if holder.reader == nil || len(holder.segmentData) == 0 {
		t.Errorf("expected segmentData not empty")
	}

	// 验证查询功能
	desc1 := &core.FieldDesc{ID: 1, Field: "field1"}

	// Case 1: 查询存在的 core.Term1
	cursors, err := holder.GetEntries(desc1, core.Values(int64(100)))
	if err != nil {
		t.Errorf("GetEntries failed: %v", err)
	}
	if len(cursors) != 1 {
		t.Errorf("expected 1 cursor, got %d", len(cursors))
	} else {
		// 因为我们现在是 Compressed 模式，GetEntries 返回的是 Cursor，
		// Cursor 内部维护状态，entries 字段可能是空的或者不直接暴露。
		// 需要通过 SkipTo 遍历出所有 Entry 来验证长度。
		cur := cursors[0]
		var decodedEntries core.Entries
		eid := cur.Current()
		for !eid.IsNULLEntry() {
			decodedEntries = append(decodedEntries, eid)
			eid = cur.SkipTo(eid + 1)
		}
		if len(decodedEntries) != len(entries1) {
			t.Errorf("expected entries len %d, got %d", len(entries1), len(decodedEntries))
		}
		if decodedEntries[0] != entries1[0] {
			t.Errorf("expected entry[0] %v, got %v", entries1[0], decodedEntries[0])
		}
	}

	// Case 2: 查询存在的 core.Term2
	cursors, err = holder.GetEntries(desc1, core.Values(int64(200)))
	if err != nil {
		t.Errorf("GetEntries failed: %v", err)
	}
	if len(cursors) != 1 {
		t.Errorf("expected 1 cursor, got %d", len(cursors))
	} else {
		cur := cursors[0]
		var decodedEntries core.Entries
		eid := cur.Current()
		for !eid.IsNULLEntry() {
			decodedEntries = append(decodedEntries, eid)
			eid = cur.SkipTo(eid + 1)
		}
		if len(decodedEntries) != len(entries2) {
			t.Errorf("expected entries len %d, got %d", len(entries2), len(decodedEntries))
		}
	}

	// Case 3: 查询不存在的 Value
	cursors, err = holder.GetEntries(desc1, core.Values(int64(300)))
	if err != nil {
		t.Errorf("GetEntries failed: %v", err)
	}
	if len(cursors) != 0 {
		t.Errorf("expected 0 cursors, got %d", len(cursors))
	}

	// Case 4: 查询 FieldID 不匹配的情况
	desc2 := &core.FieldDesc{ID: 2, Field: "field2"}
	cursors, err = holder.GetEntries(desc2, core.Values(int64(50)))
	if err != nil {
		t.Errorf("GetEntries failed: %v", err)
	}
	if len(cursors) != 1 {
		t.Errorf("expected 1 cursor, got %d", len(cursors))
	} else {
		cur := cursors[0]
		var decodedEntries core.Entries
		eid := cur.Current()
		for !eid.IsNULLEntry() {
			decodedEntries = append(decodedEntries, eid)
			eid = cur.SkipTo(eid + 1)
		}
		if len(decodedEntries) != len(entries3) {
			t.Errorf("expected entries len %d, got %d", len(entries3), len(decodedEntries))
		}
	}

	// 验证 DumpInfo
	sb := &strings.Builder{}
	holder.DumpInfo(sb)
	info := sb.String()
	if !strings.Contains(info, fmt.Sprintf(`"dictSize": %d`, len(terms))) {
		t.Errorf("DumpInfo missing dictSize")
	}
}
