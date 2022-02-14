package roaringidx

import (
	"fmt"
	"strings"

	"github.com/echoface/be_indexer/util"

	"github.com/echoface/be_indexer"
)

type (
	IvtScanner struct {
		debug bool

		inited bool

		ended bool

		indexer *IvtBEIndexer

		// conjIDResults this hold temp result
		// NOTE: it's conjunction id, not document id
		conjIDResults PostingList
	}
)

func NewScanner(indexer *IvtBEIndexer) *IvtScanner {
	return &IvtScanner{
		conjIDResults: NewPostingList(),
		indexer:       indexer,
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

func (scanner *IvtScanner) WithHint(hints ...int64) {
	util.PanicIf(scanner.inited, "can't attach hint result in progress")

	hintConjIDs := make([]uint64, 0, len(hints)*scanner.indexer.docMaxConjSize)
	for _, hintID := range hints {
		for conjIdx := 0; conjIdx < scanner.indexer.docMaxConjSize; conjIdx++ {
			conjID, err := NewConjunctionID(conjIdx, hintID)
			if err != nil {
				continue
			}
			hintConjIDs = append(hintConjIDs, uint64(conjID))
		}
	}
	scanner.conjIDResults.AddMany(hintConjIDs)
	if scanner.debug {
		be_indexer.Logger.Infof("init with hints:%s", FormatBitMapResult(scanner.conjIDResults.ToArray()))
	}
	scanner.inited = true
}

func (scanner *IvtScanner) SetDebug(debugOn bool) {
	scanner.debug = debugOn
}

func (scanner *IvtScanner) Reset() {
	scanner.inited = false
	scanner.ended = false
	scanner.debug = false

	scanner.conjIDResults.Clear()
}

// GetRawResult return the raw conjunction id result
// for some cases, this will be useful for users to judge
// which boolean condition matched in document when one document has many condition group(CONJUNCTION/DNF)
func (scanner *IvtScanner) GetRawResult() *PostingList {
	return &scanner.conjIDResults
}

func (scanner *IvtScanner) mergeFieldResult(field be_indexer.BEField, pl PostingList) {
	defer func() {
		if scanner.debug {
			be_indexer.Logger.Infof("merger result from field:%s pl:%s \n after:%s",
				field, FormatBitMapResult(pl.ToArray()), FormatBitMapResult(scanner.conjIDResults.ToArray()))
		}
		scanner.ended = scanner.conjIDResults.IsEmpty()
	}()
	if scanner.Ended() {
		return
	}
	if !scanner.inited {
		scanner.inited = true
		scanner.conjIDResults.Or(pl.Bitmap)
		return
	}
	scanner.conjIDResults.And(pl.Bitmap)
}

func (scanner *IvtScanner) retrieve(assigns be_indexer.Assignments) (err error) {
	tmpPl := NewPostingList()

	for field, can := range scanner.indexer.data {
		if scanner.ended {
			break
		}
		values := assigns[field]

		if err = can.Retrieve(values, &tmpPl); err != nil {
			return err
		}

		scanner.mergeFieldResult(field, tmpPl)
		tmpPl.Clear()
	}

	ReleasePostingList(tmpPl)
	return nil
}

func (scanner *IvtScanner) Ended() bool {
	return scanner.inited && scanner.conjIDResults.IsEmpty()
}

// RetrieveDocs return document id as map
func (scanner *IvtScanner) RetrieveDocs(assignments be_indexer.Assignments) (docs map[int64]struct{}, err error) {
	if err = scanner.retrieve(assignments); err != nil {
		return nil, err
	}
	docs = make(map[int64]struct{})

	iter := scanner.conjIDResults.Iterator()
	for iter.HasNext() {
		conjID := ConjunctionID(iter.Next())
		docs[conjID.DocID()] = struct{}{}
	}
	return docs, nil
}

// Retrieve return document id list
func (scanner *IvtScanner) Retrieve(assignments be_indexer.Assignments) (docs []uint64, err error) {
	if err = scanner.retrieve(assignments); err != nil {
		return nil, err
	}

	docBits := NewPostingList()
	iter := scanner.conjIDResults.Iterator()
	for iter.HasNext() {
		conjID := ConjunctionID(iter.Next())
		docBits.Add(uint64(conjID.DocID()))
	}
	docs = docBits.ToArray()
	ReleasePostingList(docBits)

	return docs, nil
}
