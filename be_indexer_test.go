package beindexer

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"testing"
)

func buildTestDoc() []*Document {

	//doc := NewDocument(1)
	//
	//conj := NewConjunction().
	//	In("age", []int{1, 2, 5}).
	//	In("city", []string{"sh", "bj"}).
	//	NotIn("ip", "localhost")
	//doc.AddConjunction(conj)
	//
	//conj = NewConjunction().
	//	In("age", 5).
	//	In("ip", "127.0.0.1")
	//doc.AddConjunction(conj)
	//
	//// doc2
	//doc2 := NewDocument(2)
	//conj = NewConjunction().
	//	NotIn("city", "sh").
	//	In("age", 5)
	//doc2.AddConjunction(conj)
	//
	//// doc3
	//doc3 := NewDocument(3)
	//conj = NewConjunction().
	//	NotIn("city", "sh")
	//doc3.AddConjunction(conj)
	//
	//conj = NewConjunction().
	//	In("age", []int{1, 2}).
	//	In("city", []string{"sh", "bj"})
	//doc3.AddConjunction(conj)

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
}
