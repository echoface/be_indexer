package main

import (
	"bufio"
	"fmt"
	"github.com/echoface/be_indexer"
	parser "github.com/echoface/be_indexer/parser/v2"
	"github.com/echoface/be_indexer/roaringidx"
	"io"
	"log"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"
)

type (
	benchData struct {
		indexer  *roaringidx.IvtBEIndexer
		numbers  []int
		keywords []string
	}
)

func createTestIndexer(numberFieldCnt, acMatchFieldCnt, docCount int) (*benchData, error) {
	builder := roaringidx.NewIndexerBuilder()
	fmt.Println("start configure builder .........")
	for i := 0; i < numberFieldCnt; i++ {
		fieldName := fmt.Sprintf("number_%d", i)
		builder.ConfigureField(fieldName, roaringidx.FieldSetting{
			Parser:    parser.ParserNameNumber,
			Container: "default",
		})
	}
	for i := 0; i < acMatchFieldCnt; i++ {
		fieldName := fmt.Sprintf("ac_%d", i)
		builder.ConfigureField(fieldName, roaringidx.FieldSetting{
			Container: "ac_matcher",
		})
	}

	fmt.Println("start load 10w keywords .........")
	data := &benchData{
		keywords: loadTestDataKeywords(100000),
	}

	fmt.Println("start gen test numbers .........")
	t := map[int]struct{}{}
	for len(t) < 100000 {
		t[rand.Int()] = struct{}{}
	}
	data.numbers = make([]int, 0, len(t))
	for v, _ := range t {
		data.numbers = append(data.numbers, v)
	}
	t = nil //clear for free memory

	kwsCnt := len(data.keywords)
	numCnt := len(data.numbers)

	fmt.Println("start create test document .........")
	for i := 1; i <= docCount; i++ {
		doc := roaringidx.NewDocument(int64(i))
		rn := rand.Intn(100)

		if rn < 50 { // keywords
			wordsCnt := rand.Intn(99) + 1
			start := rand.Intn(kwsCnt - wordsCnt)

			field := fmt.Sprintf("ac_%d", rand.Intn(acMatchFieldCnt))
			values := be_indexer.NewStrValues(data.keywords[start : wordsCnt+start]...)
			doc.AddConjunction(roaringidx.NewConjunction().AddExpression3(field, rn < 25, values))
		} else { // number
			wordsCnt := rand.Intn(99) + 1
			start := rand.Intn(numCnt - wordsCnt)

			field := fmt.Sprintf("number_%d", rand.Intn(numberFieldCnt))
			values := be_indexer.NewIntValues(data.numbers[start : wordsCnt+start]...)
			doc.AddConjunction(roaringidx.NewConjunction().AddExpression3(field, rn > 75, values))
		}

		builder.AddDocument(doc)
	}
	var err error

	fmt.Println("start build indexer .........")
	data.indexer, err = builder.BuildIndexer()
	return data, err
}

// topN: -1: all, n > 0: TOPN
func loadTestDataKeywords(topN int) (r []string) {
	f, _ := os.Open("testdata/30wdict.txt")
	scanner := bufio.NewReader(f)
	defer f.Close()
	for {
		data, _, err := scanner.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("read test data fail, err:", err.Error())
			break
		}
		r = append(r, string(data))
		if topN > 0 && len(r) >= topN {
			break
		}
	}
	return
}

func main() {
	testDocs := 100000
	queriesCnt := 5000
	numberFields := 5
	acMatchFields := 5

	fmt.Println("start build indexer .........")
	data, err := createTestIndexer(numberFields, acMatchFields, testDocs)
	if err != nil {
		panic(fmt.Errorf("create indexer fail:%s", err.Error()))
	}

	fmt.Println("start build query assigns .........")
	queries := []be_indexer.Assignments{}
	for i := 0; i < queriesCnt; i++ {
		q := be_indexer.Assignments{}
		for n := 0; n < numberFields; n++ {
			cnt := rand.Intn(4)
			if cnt == 0 {
				continue
			}
			start := rand.Intn(len(data.numbers) - cnt)
			end := start + cnt

			fieldName := fmt.Sprintf("number_%d", n)
			q[be_indexer.BEField(fieldName)] = be_indexer.NewIntValues(data.numbers[start:end]...)
		}
		for k := 0; k < acMatchFields; k++ {
			cnt := rand.Intn(10)
			if cnt == 0 {
				continue
			}
			start := rand.Intn(len(data.keywords) - cnt*5)
			end := start + cnt*5
			fieldName := fmt.Sprintf("ac_%d", k)
			queryStr := strings.Join(data.keywords[start:end], "")

			q[be_indexer.BEField(fieldName)] = be_indexer.NewStrValues(queryStr)
		}
		queries = append(queries, q)
	}

	runtime.GC()
	runtime.GC()
	log.Println("start run retrieve....")
	_ = os.Remove("cpu_profile.out")
	cpuf, err := os.Create("cpu_profile.out")
	if err != nil {
		log.Fatal(err)
	}
	_ = pprof.StartCPUProfile(cpuf)

	tstart := time.Now()

	scanner := roaringidx.NewScanner(data.indexer)
	for _, q := range queries {
		if _, err := scanner.Retrieve(q); err != nil {
			panic(fmt.Errorf("retrieve fail:%s", err.Error()))
		}
		scanner.Reset()
	}
	duration := time.Since(tstart)

	pprof.StopCPUProfile()
	log.Println("finish run retrieve....")
	fmt.Printf("retrieve:%d times spend:%d(ms) %d(us)/ops \n",
		len(queries), duration.Milliseconds(), duration.Microseconds()/int64(len(queries)))
}
