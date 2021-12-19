package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"strings"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/util"
)

type (
	docBuilder func(doc *be_indexer.Document)
)

func createTestIndexer(data *benchmarkContext, docFn docBuilder) {

	keywords := loadTestDataKeywords(100000)

	fmt.Println("start gen test numbers .........")
	t := map[int]struct{}{}
	for len(t) < 100000 {
		t[rand.Intn(math.MaxInt32)] = struct{}{}
	}

	numbers := make([]int, 0, len(t))
	for v, _ := range t {
		numbers = append(numbers, v)
	}
	t = nil //clear for free memory

	kwsCnt := len(keywords)
	numCnt := len(numbers)
	util.PanicIf(kwsCnt == 0, "keywords load fail")
	util.PanicIf(numCnt == 0, "gen random numbers fail")

	fmt.Println("start create test document .........")
	for i := 1; i <= data.docCount; i++ {
		doc := be_indexer.NewDocument(be_indexer.DocID(i))
		rn := rand.Intn(100)

		if rn < 50 { // keywords
			wordsCnt := rand.Intn(99) + 1
			start := rand.Intn(kwsCnt - wordsCnt)

			field := fmt.Sprintf("ac_%d", rand.Intn(data.acFieldCnt))
			values := be_indexer.NewStrValues(keywords[start : wordsCnt+start]...)
			doc.AddConjunction(be_indexer.NewConjunction().AddExpression3(field, rn < 25, values))
		} else { // number
			wordsCnt := rand.Intn(99) + 1
			start := rand.Intn(numCnt - wordsCnt)

			field := fmt.Sprintf("number_%d", rand.Intn(data.numFieldCnt))
			values := be_indexer.NewIntValues(numbers[start : wordsCnt+start]...)
			doc.AddConjunction(be_indexer.NewConjunction().AddExpression3(field, rn > 75, values))
		}

		docFn(doc)
	}

	fmt.Println("start build query assigns .........")
	for i := 0; i < data.queryCnt; i++ {
		q := be_indexer.Assignments{}
		for n := 0; n < data.numFieldCnt; n++ {
			cnt := rand.Intn(4)
			if cnt == 0 {
				continue
			}
			start := rand.Intn(len(numbers) - cnt)
			end := start + cnt

			fieldName := fmt.Sprintf("number_%d", n)
			q[be_indexer.BEField(fieldName)] = be_indexer.NewIntValues(numbers[start:end]...)
		}
		for k := 0; k < data.acFieldCnt; k++ {
			cnt := rand.Intn(10)
			if cnt == 0 {
				continue
			}
			start := rand.Intn(len(keywords) - cnt*5)
			end := start + cnt*5
			fieldName := fmt.Sprintf("ac_%d", k)
			queryStr := strings.Join(keywords[start:end], "")

			q[be_indexer.BEField(fieldName)] = be_indexer.NewStrValues(queryStr)
		}

		data.queries = append(data.queries, q)
	}
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
		util.PanicIfErr(err, "read test data fail")

		r = append(r, string(data))
		if topN > 0 && len(r) >= topN {
			break
		}
	}
	return
}
