package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

func main() {
	builder := be_indexer.NewIndexerBuilder()
	builder.ConfigField("age", be_indexer.FieldOption{
		Parser: parser.NewNumRangeParser(),
	})

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
		_ = builder.AddDocument(doc)
	}

	indexer := builder.BuildIndex()
	sb := &strings.Builder{}
	indexer.DumpEntries(sb)
	fmt.Println(sb.String())

	res, _ := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  be_indexer.NewValues2(19),
		"city": be_indexer.NewValues2("gz"),
	})
	fmt.Println("result:", res)
	if !res.Contain(1) {
		panic(fmt.Errorf("should has result 1"))
	}
}
