package be_indexer

import (
	"github.com/echoface/be_indexer/core"
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSegmentBuilderAndReader(t *testing.T) {
	builder := NewMemSegmentBuilder()

	// 1. Add Data
	// Field 1: "tag" -> 10, 20
	// Field 2: "city" -> "sh"
	fieldID1 := uint64(1)
	fieldID2 := uint64(2)

	// core.Term: <1, "10"> -> Doc 1, 3
	builder.AddEntryWithFieldID(fieldID1, "10", core.NewEntryID(core.NewConjID(1, 0, 1), true))
	builder.AddEntryWithFieldID(fieldID1, "10", core.NewEntryID(core.NewConjID(3, 0, 1), true))

	// core.Term: <1, "20"> -> Doc 2
	builder.AddEntryWithFieldID(fieldID1, "20", core.NewEntryID(core.NewConjID(2, 0, 1), true))

	// core.Term: <2, "sh"> -> Doc 1
	builder.AddEntryWithFieldID(fieldID2, "sh", core.NewEntryID(core.NewConjID(1, 0, 1), true))

	// 2. Flush
	buf := &bytes.Buffer{}
	err := builder.Flush(buf)
	assert.NoError(t, err)
	assert.True(t, buf.Len() > 0)

	// 3. Open Reader
	reader, err := NewBlockSegmentReader(buf.Bytes())
	assert.NoError(t, err)
	assert.NotNil(t, reader)

	// 4. Query
	// Query <1, "10">
	iter, err := reader.GetPostingsWithFieldID(fieldID1, "tag", "10")
	assert.NoError(t, err)
	assert.NotNil(t, iter)
	assert.Equal(t, core.EntryID(core.NewEntryID(core.NewConjID(1, 0, 1), true)), iter.Current())
	
	// SkipTo Test
	// Skip to Doc 3
	target := core.NewEntryID(core.NewConjID(3, 0, 1), true)
	assert.Equal(t, target, iter.SkipTo(target))
	assert.Equal(t, target, iter.Current())


	// Query <1, "20">
	iter, err = reader.GetPostingsWithFieldID(fieldID1, "tag", "20")
	assert.NoError(t, err)
	assert.NotNil(t, iter)
	// Check Doc 2
	doc2 := core.NewEntryID(core.NewConjID(2, 0, 1), true)
	assert.Equal(t, doc2, iter.Current())

	// Query <2, "sh">
	iter, err = reader.GetPostingsWithFieldID(fieldID2, "city", "sh")
	assert.NoError(t, err)
	assert.NotNil(t, iter)
	doc1 := core.NewEntryID(core.NewConjID(1, 0, 1), true)
	assert.Equal(t, doc1, iter.Current())

	// Query Not Exist
	iter, err = reader.GetPostingsWithFieldID(fieldID1, "tag", "not_exist")
	assert.NoError(t, err)
	assert.Nil(t, iter)
}

