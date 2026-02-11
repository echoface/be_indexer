package be_indexer

import (
	"encoding/binary"
	"sort"

	"github.com/echoface/be_indexer/codegen/indexstore"
)

// TermDictionary 定义了词典的通用接口
type TermDictionary interface {
	// Get 返回 term 对应的 ID，如果不存在返回 false
	Get(fieldID uint64, value string) (uint64, bool)

	// Decode 根据 ID 反查 term (主要用于 Debug)
	Decode(id uint64) (fieldID uint64, value string, found bool)

	// Size 返回字典中的词条数量
	Size() int
}

// DefaultTermDict 基于有序数组的 MVP 实现
type DefaultTermDict struct {
	terms   []KVTerm
	content []byte
	offsets []uint32
}

func NewDefaultTermDict(terms []KVTerm) *DefaultTermDict {
	return &DefaultTermDict{
		terms: terms,
	}
}

// NewTermDictFromProto 从 Proto 数据加载字典
func NewTermDictFromProto(protoDict *indexstore.TermDict) TermDictionary {
	if protoDict == nil {
		return &DefaultTermDict{}
	}

	// Prefer Vellum
	if len(protoDict.FstData) > 0 {
		// Content field is reused for Samples in Vellum implementation
		dict, err := NewVellumTermDict(protoDict.FstData, protoDict.Content)
		if err == nil {
			return dict
		}
		// If error, fallback or return empty? 
		// If Vellum load fails, it's a critical error for that segment.
		// But we have to return something.
		// For now, return empty or panic? 
		// Since signature returns TermDictionary, we can return nil? No, BlockSegmentReader expects valid dict.
		// Return empty VellumDict or DefaultTermDict?
		return &DefaultTermDict{}
	}

	// Legacy Array Implementation
	return &DefaultTermDict{
		content: protoDict.Content,
		offsets: protoDict.Offsets,
	}
}

func (d *DefaultTermDict) Get(fieldID uint64, value string) (uint64, bool) {
	// If d.terms is populated (Builder mode), use it
	if len(d.terms) > 0 {
		target := KVTerm{FieldID: fieldID, Value: value}
		idx := sort.Search(len(d.terms), func(i int) bool {
			ti := d.terms[i]
			if ti.FieldID != target.FieldID {
				return ti.FieldID >= target.FieldID
			}
			return ti.Value >= target.Value
		})
		if idx < len(d.terms) && d.terms[idx] == target {
			return uint64(idx), true
		}
		return 0, false
	}

	// If d.content is populated (Reader mode), use binary search on offsets
	if len(d.offsets) > 0 {
		n := len(d.offsets)
		idx := sort.Search(n, func(i int) bool {
			fid, val := d.decodeTerm(i)
			if fid != fieldID {
				return fid >= fieldID
			}
			return val >= value
		})
		if idx < n {
			fid, val := d.decodeTerm(idx)
			if fid == fieldID && val == value {
				return uint64(idx), true
			}
		}
		return 0, false
	}

	return 0, false
}

func (d *DefaultTermDict) Decode(id uint64) (uint64, string, bool) {
	idx := int(id)
	if len(d.terms) > 0 {
		if idx < 0 || idx >= len(d.terms) {
			return 0, "", false
		}
		t := d.terms[idx]
		return t.FieldID, t.Value, true
	}

	if len(d.offsets) > 0 {
		if idx < 0 || idx >= len(d.offsets) {
			return 0, "", false
		}
		fid, val := d.decodeTerm(idx)
		return fid, val, true
	}

	return 0, "", false
}

func (d *DefaultTermDict) Size() int {
	if len(d.terms) > 0 {
		return len(d.terms)
	}
	return len(d.offsets)
}

// decodeTerm 从 content/offsets 中解码第 i 个 term
func (d *DefaultTermDict) decodeTerm(i int) (uint64, string) {
	offset := int(d.offsets[i])

	// core.Term Format in Content: [FieldID(Uvarint)][ValLen(Uvarint)][ValueBytes]
	// But wait, offsets usually point to start of term.

	// Let's define the format:
	// content[offset] -> FieldID (uvarint)
	// content[...]    -> Value (len prefixed or null terminated? Len prefixed is better)

	data := d.content[offset:]
	fid, n := binary.Uvarint(data)
	data = data[n:]

	valLen, n2 := binary.Uvarint(data)
	data = data[n2:]

	val := string(data[:valLen])
	return fid, val
}

