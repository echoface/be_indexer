package main

import (
	"fmt"

	"github.com/echoface/be_indexer"
)

func buildTestDoc() []*be_indexer.Document {
	docs := []*be_indexer.Document{}

	doc := be_indexer.NewDocument(be_indexer.DocID(1))
	conj := be_indexer.NewConjunction().
		In("age", []int{30, 31, 32}).NotIn("age", []int{33, 34})
	doc.AddConjunction(conj)
	// fmt.Println(doc.JSONString())
	docs = append(docs, doc)
	doc = be_indexer.NewDocument(be_indexer.DocID(2))
	conj = be_indexer.NewConjunction().
		In("city", []string{"beijing"}).NotIn("city", []string{"shanghai"})
	doc.AddConjunction(conj)
	// fmt.Println(doc.JSONString())
	docs = append(docs, doc)
	return docs
}

func main() {
	builder := be_indexer.NewIndexerBuilder(
		be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
	)
	// or use a compacted version, it faster about 12% than default
	// builder := be_indexer.NewCompactIndexerBuilder()

	// optional special a holder/container for field
	// can also register customized container: see: entries_holder_factory.go
	builder.ConfigField("keyword", be_indexer.FieldOption{
		Container: be_indexer.HolderNameACMatcher,
	})

	for _, doc := range buildTestDoc() {
		_ = builder.AddDocument(doc) // see: document.go for how to construct doc
	}

	indexer := builder.BuildIndex()

	// indexing satisfied docs
	assigns := map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewIntValues(33),
		"city": be_indexer.NewStrValues("beijing"),
		"tag":  be_indexer.NewStrValues("tag1", "tagn"),
	}

	result, e := indexer.Retrieve(assigns,
		be_indexer.WithStepDetail(),
		be_indexer.WithDumpEntries())
	fmt.Println(e, result)
}
