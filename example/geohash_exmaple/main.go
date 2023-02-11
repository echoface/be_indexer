package main

import (
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

func main() {
	be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.FieldParser = map[be_indexer.BEField]parser.FieldValueParser{
			"tag": parser.NewNumberParser(),
			"geo": parser.NewGeoHashParser(nil),
		}
		return holder
	})

	// tag in (1,5) && geo in (经纬度半径一公里内) && kws != "adult av")
	conj := be_indexer.NewConjunction().
		In("tag", []int64{1, 5}).
		NotIn("kws", []string{"adult"}).
		In("geo", "31.21275902:121.53779984:1000")
	doc := be_indexer.NewDocument(1)
	doc.AddConjunction(conj)

	b := be_indexer.NewCompactIndexerBuilder()
	err := b.AddDocument(doc)
	util.PanicIfErr(err, "add doc fail:%v", err)

	index := b.BuildIndex()

	be_indexer.PrintIndexInfo(index)
	be_indexer.PrintIndexEntries(index)

	results, err := index.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"tag": 1000,
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail(), be_indexer.WithDumpEntries())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(results.Len() > 0, "need empty result") // tag:1000 不满足

	results, err = index.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"tag": 100,
		"kws": "adult",
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail(), be_indexer.WithDumpEntries())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(results.Len() > 0, "need empty result") // kws:adult 不满足

	results, err = index.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"tag": 1,
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail(), be_indexer.WithDumpEntries())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(!results.Contain(1), "need has result:1") // 满足
}
