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
	"github.com/echoface/be_indexer/util"
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
		util.PanicIfErr(e, "open file:%s fail", path)

		doc := &be_indexer.Document{}
		e = json.Unmarshal(content, &doc)
		util.PanicIfErr(e, "decode document:%s fail, content:%s", path, string(content))

		docs = append(docs, doc)
		return nil
	})
	for _, doc := range docs {
		fmt.Println("add document:", doc.ID)
		err := builder.AddDocument(doc)
		util.PanicIfErr(err, "should not fail")
	}

	indexer := builder.BuildIndex()
	sb := &strings.Builder{}
	indexer.DumpEntries(sb)
	fmt.Println(sb.String())

	res, _ := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  19,
		"city": "gz",
	})
	fmt.Println("result:", res)
	if !res.Contain(1) {
		panic(fmt.Errorf("should has result 1"))
	}
}
