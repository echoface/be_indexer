package main

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
	"github.com/echoface/be_indexer/util"
)

func main() {

	builder := roaringidx.NewIndexerBuilder()

	_ = builder.ConfigureField("ad_id", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameDefault,
		Parser:    parser.NewNumberParser(),
	})
	_ = builder.ConfigureField("package", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameDefault,
		Parser:    parser.NewStrHashParser(),
	})
	_ = builder.ConfigureField("keywords", roaringidx.FieldSetting{
		Container: roaringidx.ContainerNameAcMatch,
	})

	doc1 := be_indexer.NewDocument(1)
	doc1.AddConjunction(be_indexer.NewConjunction().
		Include("ad_id", be_indexer.NewIntValues(100, 101, 108)).
		Exclude("package", be_indexer.NewStrValues("com.echoface.not")))
	doc1.AddConjunction(be_indexer.NewConjunction().
		Include("package", be_indexer.NewStrValues("com.echoface.in")))

	doc3 := be_indexer.NewDocument(20)
	doc3.AddConjunctions(be_indexer.NewConjunction())

	doc4 := be_indexer.NewDocument(50)
	doc4.AddConjunction(be_indexer.NewConjunction().
		Exclude("ad_id", be_indexer.NewIntValues(100, 108)).
		Include("package", be_indexer.NewStrValues("com.echoface.be")))

	builder.AddDocuments(doc1, doc3, doc4)

	indexer, err := builder.BuildIndexer()
	util.PanicIfErr(err, "should not err here")

	scanner := roaringidx.NewScanner(indexer)
	docs, err := scanner.Retrieve(map[be_indexer.BEField]be_indexer.Values{
		"ad_id":   []interface{}{100, 102},
		"package": []interface{}{"com.echoface.be", "com.echoface.not"},
	})
	util.PanicIfErr(err, "retrieve fail, err:%v", err)
	fmt.Println("docs:", docs)
	fmt.Println("raw result:", roaringidx.FormatBitMapResult(scanner.GetRawResult().ToArray()))
	scanner.Reset()

	// concurrency retrieve
	fmt.Println("start concurrency retrieve test")
	wg := &sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			var e error
			sc := roaringidx.NewScanner(indexer)
			for j := 0; j < 100; j++ {
				_, e = sc.Retrieve(map[be_indexer.BEField]be_indexer.Values{
					"ad_id":   []interface{}{rand.Intn(4) + 98, rand.Intn(4) + 100},
					"package": []interface{}{"com.echoface.be", "com.echoface.not"},
				})
				util.PanicIfErr(e, "retrieve fail, err:%v", e)
				scanner.Reset()
			}
		}()
	}
	time.Sleep(time.Second * 1)
	wg.Wait()
}
