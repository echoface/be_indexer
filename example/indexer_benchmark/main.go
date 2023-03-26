package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
	"github.com/echoface/be_indexer/util"
)

type (
	benchmarkContext struct {
		acFieldCnt  int
		numFieldCnt int
		docCount    int
		queryCnt    int
		queries     []be_indexer.Assignments
	}
)

var bench string
var enableCPUProfile bool
var enableHTTPProfile bool

func init() {
	flag.StringVar(&bench, "bench", "", "--bench=roaring|compact|kgroup")
	flag.BoolVar(&enableCPUProfile, "cpu", false, "--cpu=true to enable cpu profiling")
	flag.BoolVar(&enableHTTPProfile, "http", false, "--http=true to enable profiling base http")
}

func NewBenchContext(acCnt, numCnt, docCnt, queryCnt int) *benchmarkContext {
	ctx := &benchmarkContext{
		acFieldCnt:  acCnt,
		numFieldCnt: numCnt,
		docCount:    docCnt,
		queryCnt:    queryCnt,
		queries:     nil,
	}
	return ctx
}

func (ctx *benchmarkContext) enableCPUProfile() {
	if !enableCPUProfile {
		fmt.Println("cpu profile switch off, skip cpu profiling")
		return
	}
	_ = os.Remove("cpu_profile.out")
	cpuf, err := os.Create("cpu_profile.out")
	if err != nil {
		log.Fatal(err)
	}
	_ = pprof.StartCPUProfile(cpuf)
}

func (ctx *benchmarkContext) stopCPUProfile() {
	if !enableCPUProfile {
		return
	}
	pprof.StopCPUProfile()
}

func (ctx *benchmarkContext) RunRoaringBench() {
	builder := roaringidx.NewIndexerBuilder()
	fmt.Println("start configure roaringidx builder .........")
	for i := 0; i < ctx.numFieldCnt; i++ {
		fieldName := fmt.Sprintf("number_%d", i)
		_ = builder.ConfigureField(fieldName, roaringidx.FieldSetting{
			Parser:    parser.NewNumberParser(),
			Container: "default",
		})
	}
	for i := 0; i < ctx.acFieldCnt; i++ {
		fieldName := fmt.Sprintf("ac_%d", i)
		_ = builder.ConfigureField(fieldName, roaringidx.FieldSetting{
			Container: "ac_matcher",
		})
	}
	createTestIndexer(ctx, func(doc *be_indexer.Document) {
		_ = builder.AddDocument(doc)
	})

	idxer, err := builder.BuildIndexer()
	util.PanicIfErr(err, "build roaring indexer fail")
	util.PanicIf(len(ctx.queries) != ctx.queryCnt, "query cnt not match")

	runtime.GC()

	ctx.enableCPUProfile()
	fmt.Println("start bench roaring retrieve...")

	scanner := roaringidx.NewScanner(idxer)
	tn := time.Now()
	for _, assigns := range ctx.queries {
		if _, err = scanner.Retrieve(assigns); err != nil {
			util.PanicIfErr(err, "retrieve fail, %s", util.JSONPretty(assigns))
		}
		scanner.Reset()
	}
	duration := time.Since(tn)
	fmt.Printf("spend:%d(ms) %d us/ops\n", duration.Milliseconds(), duration.Microseconds()/int64(ctx.queryCnt))

	ctx.stopCPUProfile()
}

func (ctx *benchmarkContext) RunKGroupIndexBench() {
	be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.FieldParser = map[be_indexer.BEField]parser.FieldValueParser{}
		numberParser := parser.NewNumberParser()
		for i := 0; i < ctx.numFieldCnt; i++ {
			fieldName := fmt.Sprintf("number_%d", i)
			holder.FieldParser[be_indexer.BEField(fieldName)] = numberParser
		}
		return holder
	})
	builder := be_indexer.NewIndexerBuilder()
	for i := 0; i < ctx.numFieldCnt; i++ {
		fieldName := fmt.Sprintf("number_%d", i)
		builder.ConfigField(be_indexer.BEField(fieldName), be_indexer.FieldOption{
			Container: be_indexer.HolderNameDefault,
		})
	}
	for i := 0; i < ctx.acFieldCnt; i++ {
		fieldName := fmt.Sprintf("ac_%d", i)
		builder.ConfigField(be_indexer.BEField(fieldName), be_indexer.FieldOption{
			Container: be_indexer.HolderNameACMatcher,
		})
	}
	var err error
	createTestIndexer(ctx, func(doc *be_indexer.Document) {
		err = builder.AddDocument(doc)
		util.PanicIfErr(err, "kgroup indexer resolve doc fail")
	})

	indexer := builder.BuildIndex()
	be_indexer.PrintIndexInfo(indexer)

	util.PanicIf(len(ctx.queries) != ctx.queryCnt, "query cnt not match")
	runtime.GC()

	fmt.Println("start bench kgroup indexer retrieve...")
	ctx.enableCPUProfile()
	tn := time.Now()
	for _, assigns := range ctx.queries {
		if _, err = indexer.Retrieve(assigns); err != nil {
			util.PanicIfErr(err, "retrieve fail, %s", util.JSONPretty(assigns))
		}
	}
	duration := time.Since(tn)
	fmt.Printf("spend:%d(ms) %d us/ops\n", duration.Milliseconds(), duration.Microseconds()/int64(ctx.queryCnt))
	ctx.stopCPUProfile()
}

func (ctx *benchmarkContext) RunCompactIndexBench() {
	be_indexer.RegisterEntriesHolder(be_indexer.HolderNameDefault, func() be_indexer.EntriesHolder {
		holder := be_indexer.NewDefaultEntriesHolder()
		holder.FieldParser = map[be_indexer.BEField]parser.FieldValueParser{}
		numberParser := parser.NewNumberParser()
		for i := 0; i < ctx.numFieldCnt; i++ {
			fieldName := fmt.Sprintf("number_%d", i)
			holder.FieldParser[be_indexer.BEField(fieldName)] = numberParser
		}
		return holder
	})

	builder := be_indexer.NewCompactIndexerBuilder()

	for i := 0; i < ctx.numFieldCnt; i++ {
		fieldName := fmt.Sprintf("number_%d", i)
		builder.ConfigField(be_indexer.BEField(fieldName), be_indexer.FieldOption{
			Container: be_indexer.HolderNameDefault,
		})
	}
	for i := 0; i < ctx.acFieldCnt; i++ {
		fieldName := fmt.Sprintf("ac_%d", i)
		builder.ConfigField(be_indexer.BEField(fieldName), be_indexer.FieldOption{
			Container: be_indexer.HolderNameACMatcher,
		})
	}
	var err error
	createTestIndexer(ctx, func(doc *be_indexer.Document) {
		err = builder.AddDocument(doc)
		util.PanicIfErr(err, "kgroup indexer resolve doc fail")
	})

	indexer := builder.BuildIndex()
	util.PanicIf(len(ctx.queries) != ctx.queryCnt, "query cnt not match")
	runtime.GC()
	be_indexer.PrintIndexInfo(indexer)

	ctx.enableCPUProfile()
	tn := time.Now()

	fmt.Println("start bench compact indexer retrieve...")
	for _, assigns := range ctx.queries {
		if _, err = indexer.Retrieve(assigns); err != nil {
			util.PanicIfErr(err, "retrieve fail, %s", util.JSONPretty(assigns))
		}
	}
	duration := time.Since(tn)
	fmt.Printf("spend:%d(ms) %d us/ops\n", duration.Milliseconds(), duration.Microseconds()/int64(ctx.queryCnt))
	ctx.stopCPUProfile()
}

func main() {
	flag.Parse()
	be_indexer.LogLevel = be_indexer.ErrorLevel

	testDocs := 1000000
	queriesCnt := 1000
	numberFields := 5
	acMatchFields := 5
	notice := `this will test 100w document with 10 fields(five default, five base on ac-matcher)
each document contain one conjunction and each conjunction field has 50 values on average`
	fmt.Println(notice)

	if enableHTTPProfile {
		go func() {
			_ = http.ListenAndServe("localhost:6061", nil)
			fmt.Println("http profile on port:6061, ctrl-c to end this program")
		}()
	}

	ctx := NewBenchContext(acMatchFields, numberFields, testDocs, queriesCnt)

	switch bench {
	case "roaring":
		ctx.RunRoaringBench()
	case "kgroup":
		ctx.RunKGroupIndexBench()
	case "compact":
		ctx.RunCompactIndexBench()
	default:
		fmt.Println("please spec args: --bench=roaring|compact|kgroup")
		return
	}

	for {
		runtime.GC()
		time.Sleep(time.Second * 5)
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		fmt.Println("alloc:", ms.Alloc/1024, "kb")
	}
}
