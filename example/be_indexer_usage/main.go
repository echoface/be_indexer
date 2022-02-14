package main

import (
	"fmt"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
)

func buildTestDoc() []*be_indexer.Document {
	return []*be_indexer.Document{}
}

func main() {
	builder := be_indexer.NewIndexerBuilder()
	// or use a compacted version, it faster about 12% than default
	// builder := be_indexer.NewCompactIndexerBuilder()

	// optional special a holder/container for field
	builder.ConfigField("keyword", be_indexer.FieldOption{
		Container: be_indexer.HolderNameACMatcher,
	})

	for _, doc := range buildTestDoc() {
		err := builder.AddDocument(doc)
		util.PanicIfErr(err, "document can't be resolved")
	}

	indexer := builder.BuildIndex()

	result, e := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age": be_indexer.NewValues2(5),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"ip": be_indexer.NewStrValues2("localhost"),
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewIntValues2(1),
		"city": be_indexer.NewStrValues2("sh"),
		"tag":  be_indexer.NewValues2("tag1"),
	}, be_indexer.WithStepDetail(), be_indexer.WithDumpEntries())
	fmt.Println(e, result)
}
