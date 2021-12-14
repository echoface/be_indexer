package main

import (
	"fmt"
	"github.com/echoface/be_indexer"
)

func buildTestDoc() []*be_indexer.Document {
	return []*be_indexer.Document{}
}

func main() {
	builder := be_indexer.IndexerBuilder{
		Documents: make(map[be_indexer.DocID]*be_indexer.Document),
	}

	for _, doc := range buildTestDoc() {
		builder.AddDocument(doc)
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
	})
	fmt.Println(e, result)
}
