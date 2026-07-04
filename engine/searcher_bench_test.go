package engine_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/segment"
)

func buildBenchEngine(b *testing.B, fields map[core.BEField]*core.FieldMeta, docs []*core.Document) *engine.BooleanEngine {
	b.Helper()
	buf := new(bytes.Buffer)
	wildcards, err := builder.BuildSegmentFromDocs(buf, fields, docs)
	if err != nil {
		b.Fatalf("BuildSegmentFromDocs failed: %v", err)
	}
	seg, err := segment.NewSegmentReader(buf.Bytes())
	if err != nil {
		b.Fatalf("NewSegmentReader failed: %v", err)
	}
	eng, err := engine.NewBooleanEngine(fields, wildcards, []*segment.SegmentReader{seg})
	if err != nil {
		b.Fatalf("NewBooleanEngine failed: %v", err)
	}
	return eng
}

// BenchmarkRetrieveHighK exercises the hot retrieve path with a multi-field
// query (high maxK). Query encoding is now hoisted out of the K loop, so this
// benchmark is the primary signal for the encoder/encode-once optimization.
func BenchmarkRetrieveHighK(b *testing.B) {
	const numFields = 8
	fields := map[core.BEField]*core.FieldMeta{}
	for i := 0; i < numFields; i++ {
		name := fmt.Sprintf("f%d", i)
		fields[name] = &core.FieldMeta{ID: uint64(i + 1), Field: name, FieldOption: core.FieldOption{Tokenizer: "number"}}
	}

	// Documents: each conjunction includes every field, producing high-K conjunctions.
	docs := make([]*core.Document, 0, 2000)
	for d := 0; d < 2000; d++ {
		doc := core.NewDocument(core.DocID(d + 1))
		conj := core.NewConjunction()
		for i := 0; i < numFields; i++ {
			conj.Include(fmt.Sprintf("f%d", i), (d+i)%50)
		}
		doc.AddConjunction(conj)
		docs = append(docs, doc)
	}

	eng := buildBenchEngine(b, fields, docs)

	query := core.Assignments{}
	for i := 0; i < numFields; i++ {
		query[fmt.Sprintf("f%d", i)] = (i + 7) % 50
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := eng.Retrieve(query); err != nil {
			b.Fatal(err)
		}
	}
}
