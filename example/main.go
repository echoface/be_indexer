package main

import (
	beindexer "be_indexer"
	"be_indexer/parser"
	"be_indexer/util"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	builder := beindexer.NewIndexerBuilder()

	var docs []*beindexer.Document
	filepath.Walk("./docs", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		content, e := ioutil.ReadFile(path)
		if e != nil {
			fmt.Println("read file fail:", content)
			return nil
		}
		doc := &beindexer.Document{}
		if e = json.Unmarshal(content, &doc); e != nil {
			fmt.Println("decode document fail:", e.Error())
			return e
		}
		docs = append(docs, doc)
		return nil
	})
	for _, doc := range docs {
		fmt.Println("add document:", doc.ID)
		builder.AddDocument(doc)
	}
	builder.SetFieldParser("age", parser.NumRangeParser)

	indexer := builder.BuildIndex()
	fmt.Println(indexer.DumpSizeEntries())

	res, _ := indexer.Retrieve(map[beindexer.BEField]beindexer.Values{
		"age":  beindexer.NewValues(19),
		"city": beindexer.NewValues("gz"),
	})
	fmt.Println("result:", res)
	if !util.ContainInt32(res, 1) {
		panic(fmt.Errorf("should has result 1"))
	}
}
