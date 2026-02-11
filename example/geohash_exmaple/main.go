package main

import (
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

func main() {
	be_indexer.RegisterFieldBuilder(core.HolderNameDefault, func() core.FieldIndexBuilder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.RegisterFieldTokenizer("tag", parser.NewNumberParser())
		holder.RegisterFieldTokenizer("geo", parser.NewGeoHashParser(nil))
		return holder
	})

	// tag in (1,5) && geo in (经纬度半径一公里内) && kws != "adult av")
	conj := core.NewConjunction().
		In("tag", []int64{1, 5}).
		NotIn("kws", []string{"adult"}).
		In("geo", "31.21275902:121.53779984:1000")
	doc := core.NewDocument(1)
	doc.AddConjunction(conj)

	b := be_indexer.NewCompactIndexerBuilder()
	err := b.AddDocument(doc)
	util.PanicIfErr(err, "add doc fail:%v", err)

	index, err := b.BuildIndex()
	util.PanicIfErr(err, "build index fail")

	be_indexer.PrintIndexInfo(index)

	results, err := index.Retrieve(map[core.BEField]core.Values{
		"tag": 1000,
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(results.Len() > 0, "need empty result") // tag:1000 不满足

	results, err = index.Retrieve(map[core.BEField]core.Values{
		"tag": 100,
		"kws": "adult",
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(results.Len() > 0, "need empty result") // kws:adult 不满足

	results, err = index.Retrieve(map[core.BEField]core.Values{
		"tag": 1,
		"geo": [2]float64{31.21275902, 121.53779984},
	}, be_indexer.WithStepDetail())
	util.PanicIfErr(err, "failed retrieve")
	util.PanicIf(!results.Contain(1), "need has result:1") // 满足
}
