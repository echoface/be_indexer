package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

func main() {
	be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.FieldParser = map[be_indexer.BEField]parser.FieldValueParser{
			"age": parser.NewNumRangeParser(),
		}
		return holder
	})
	builder := be_indexer.NewIndexerBuilder()
	be_indexer.LogLevel = be_indexer.DebugLevel

	var docs []*be_indexer.Document
	_ = filepath.Walk("./docs", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		content, e := os.ReadFile(path)
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
	be_indexer.PrintIndexInfo(indexer)
	be_indexer.PrintIndexEntries(indexer)

	res, _ := indexer.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"age":  19,
		"city": "gz",
	}, be_indexer.WithDumpEntries(), be_indexer.WithStepDetail())
	fmt.Println("result:", res)
	if !res.Contain(1) {
		panic(fmt.Errorf("should has result 1"))
	}
}
