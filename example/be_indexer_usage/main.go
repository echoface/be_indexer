package main

import (
	"github.com/echoface/be_indexer/core"
	"fmt"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
)

func buildTestDoc() []*core.Document {
	return []*core.Document{}
}

func main() {
	builder := be_indexer.NewIndexerBuilder(
		be_indexer.WithBadConjBehavior(be_indexer.SkipBadConj),
	)
	// or use a compacted version, it faster about 12% than default
	// builder := be_indexer.NewCompactIndexerBuilder()

	// optional special a holder/container for field
	// dever can also register customized container: see: entries_holder_factory.go
	builder.ConfigField("keyword", core.FieldOption{
		Container: core.HolderNameACMatcher,
	})

	for _, doc := range buildTestDoc() {
		_ = builder.AddDocument(doc)
	}

	indexer, err := builder.BuildIndex()
	util.PanicIfErr(err, "build index fail")

	// indexing satisfied docs
	assigns := map[core.BEField]core.Values{
		"age":  core.NewIntValues(1),
		"city": core.NewStrValues("sh", "bj"),
		"tag":  core.NewStrValues("tag1", "tagn"),
	}

	result, e := indexer.Retrieve(assigns,
		be_indexer.WithStepDetail())
	fmt.Println(e, result)
}
