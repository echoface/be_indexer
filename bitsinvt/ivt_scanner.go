package bitsinvt

import (
	"fmt"
	"github.com/echoface/be_indexer"
	"strings"
)

type (
	IvtScanner struct {
		indexer *IvtBEIndexer
	}
)

func NewScanner(indexer *IvtBEIndexer) *IvtScanner {
	return &IvtScanner{
		indexer: indexer,
	}
}

func FormatBitMapResult(ids []uint64) string {
	var vs []string
	for _, id := range ids {
		conjID := ConjunctionID(id)
		vs = append(vs, fmt.Sprintf("[%d,%d]", conjID.DocID(), conjID.Idx()))
	}
	return strings.Join(vs, ",")
}

func (scanner *IvtScanner) RetrieveDocs(assignments be_indexer.Assignments) (docs []int64, err error) {
	conjIDs, e := scanner.Retrieve(assignments)
	if e != nil {
		return nil, e
	}
	docs = make([]int64, len(conjIDs))
	for idx, conjID := range conjIDs {
		docs[idx] = ConjunctionID(conjID).DocID()
	}
	return docs, nil
}

func (scanner *IvtScanner) Retrieve(assignments be_indexer.Assignments) ([]uint64, error) {
	resultMerger := NewPostingList()

	var inited bool = false
	merger := NewPostingList()

	for fieldName, data := range scanner.indexer.fields {
		if inited && resultMerger.IsEmpty() {
			fmt.Println(":", fieldName)
			return resultMerger.ToArray(), nil
		}
		fmt.Println("start retrieve field:", fieldName)

		container := data.indexPl

		merger.Clear()
		merger.Or(container.wc.Bitmap)

		fmt.Println(fieldName, ",wc:", FormatBitMapResult(merger.ToArray()))

		fieldAssign, ok := assignments[fieldName]
		if !ok || len(fieldAssign) == 0 {
			if !inited {
				inited = true
				resultMerger.Or(merger.Bitmap)
			}
			resultMerger.And(merger.Bitmap)
			continue
		}

		var fieldIDs []uint64
		for _, vi := range fieldAssign {
			fmt.Printf("field:%s value:%+v\n", fieldName, vi)
			ids, err := data.parser.ParseAssign(vi)
			if err != nil {
				fmt.Printf("parser value fail, field:%s value:%+v\n", fieldName, vi)
				return nil, err
			}
			fieldIDs = append(fieldIDs, ids...)
		}
		for _, id := range fieldIDs {
			if incPl, ok := container.inc[BEValue(id)]; ok {
				merger.Or(incPl.Bitmap)
				fmt.Println(fieldName, ",merge:", FormatBitMapResult(incPl.Bitmap.ToArray()))
				fmt.Println(fieldName, ",inc result:", FormatBitMapResult(merger.ToArray()))
			}
		}
		for _, id := range fieldIDs {
			if excPl, ok := container.exc[BEValue(id)]; ok {
				merger.AndNot(excPl.Bitmap)
				fmt.Println(fieldName, ",sub:", FormatBitMapResult(excPl.Bitmap.ToArray()))
				fmt.Println(fieldName, ",exc result:", FormatBitMapResult(merger.ToArray()))
			}
		}
		if !inited {
			inited = true
			resultMerger.Or(merger.Bitmap)
		}
		resultMerger.And(merger.Bitmap)
		fmt.Println(fieldName, ",result:", FormatBitMapResult(resultMerger.ToArray()))
	}
	return resultMerger.ToArray(), nil
}
