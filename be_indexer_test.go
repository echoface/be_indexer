package beindexer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"testing"
)

func buildTestDoc() []*Document {

	docs := make([]*Document, 0)
	content, e := ioutil.ReadFile("./test_data/test_docs.json")
	if e != nil {
		panic(e)
	}
	if e = json.Unmarshal(content, &docs); e != nil {
		panic(e)
	}
	fmt.Println("total docs:", len(docs))
	return docs
}

func EntriesToDocs(entries Entries) (res []int32) {
	for _, eid := range entries {
		res = append(res, eid.GetConjID().DocID())
	}
	return
}

func TestBEIndex_Retrieve(t *testing.T) {
	builder := IndexerBuilder{
		Documents: make(map[int32]*Document),
	}

	for _, doc := range buildTestDoc() {
		builder.AddDocument(doc)
	}

	indexer := builder.BuildIndex()

	fmt.Println(indexer.DumpSizeEntries())

	result, e := indexer.Retrieve(map[BEField]Values{
		"age": []int{5},
	})
	fmt.Println(e, result)

	result, e = indexer.Retrieve(map[BEField]Values{
		"age":  []int{1},
		"city": "sh",
		"tag:": "tag2",
	})
	fmt.Println(e, result)

}
