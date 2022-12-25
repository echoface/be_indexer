package main

import (
	"fmt"

	"github.com/echoface/be_indexer"
)

func buildTestDoc() []*be_indexer.Document {
	return []*be_indexer.Document{}
}

func main() {
	builder := be_indexer.NewIndexerBuilder(
		be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
	)
	// or use a compacted version, it faster about 12% than default
	// builder := be_indexer.NewCompactIndexerBuilder()

	// optional special a holder/container for field
	// dever can also register customized container: see: entries_holder_factory.go
	builder.ConfigField("keyword", be_indexer.FieldOption{
		Container: be_indexer.HolderNameACMatcher,
	})

	for _, doc := range buildTestDoc() {
		_ = builder.AddDocument(doc)
	}

	indexer := builder.BuildIndex()

	// indexing satisfied docs
	assigns := map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewIntValues(1),
		"city": be_indexer.NewStrValues("sh", "bj"),
		"tag":  be_indexer.NewStrValues("tag1", "tagn"),
	}

	result, e := indexer.Retrieve(assigns,
		be_indexer.WithStepDetail(),
		be_indexer.WithDumpEntries())
	fmt.Println(e, result)
}
