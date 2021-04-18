package main

import (
	"encoding/json"
	"fmt"
	"github.com/HuanGong/be_indexer"
	"github.com/HuanGong/be_indexer/parser"
	"github.com/HuanGong/be_indexer/util"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	builder := be_indexer.NewIndexerBuilder()

	var docs []*be_indexer.Document
	_ = filepath.Walk("./docs", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		content, e := ioutil.ReadFile(path)
		if e != nil {
			fmt.Println("read file fail:", content)
			return nil
		}
		doc := &be_indexer.Document{}
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

	res, _ := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewValues2(19),
		"city": be_indexer.NewValues2("gz"),
	})
	fmt.Println("result:", res)
	if !util.ContainInt32(res, 1) {
		panic(fmt.Errorf("should has result 1"))
	}
}
