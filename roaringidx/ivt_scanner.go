package roaringidx

import (
	"fmt"
	"github.com/echoface/be_indexer/util"
	"strings"

	"github.com/echoface/be_indexer"
)

type (
	IvtScanner struct {
		inited  bool
		ended   bool
		results PostingList
		indexer *IvtBEIndexer
		debug   bool
	}
)

func NewScanner(indexer *IvtBEIndexer) *IvtScanner {
	return &IvtScanner{
		results: NewPostingList(),
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

func (scanner *IvtScanner) WithHint(hint ...uint64) {
	util.PanicIf(scanner.inited, "can't atatch hint result in progress")
	scanner.results.AddMany(hint)
	scanner.inited = true
}

func (scanner *IvtScanner) SetDebug(debugOn bool) {
	scanner.debug = debugOn
}

func (scanner *IvtScanner) Reset() {
	scanner.inited = false
	scanner.ended = false
	scanner.results.Clear()
	scanner.debug = false
}

func (scanner *IvtScanner) MergeFieldResult(field be_indexer.BEField, pl PostingList) {
	defer func() {
		if scanner.debug {
			be_indexer.Logger.Infof("merger result from field:%s pl:%s \n after:%s",
				field, FormatBitMapResult(pl.ToArray()), FormatBitMapResult(scanner.results.ToArray()))
		}
	}()
	if !scanner.inited {
		scanner.results.Or(pl.Bitmap)
		scanner.inited = true
		return
	}

	if scanner.ended {
		return
	}
	scanner.results.And(pl.Bitmap)
	scanner.ended = scanner.results.IsEmpty()
}

func (scanner *IvtScanner) RetrieveDocs(assignments be_indexer.Assignments) (docs map[int64]struct{}, err error) {
	conjIDs, e := scanner.Retrieve(assignments)
	if e != nil {
		return nil, e
	}
	docs = make(map[int64]struct{})
	for _, conjID := range conjIDs {
		docs[ConjunctionID(conjID).DocID()] = struct{}{}
	}
	return docs, nil
}

func (scanner *IvtScanner) Retrieve(assignments be_indexer.Assignments) ([]uint64, error) {
	var err error
	pl := NewPostingList()

	for field, fieldData := range scanner.indexer.data {
		if scanner.ended {
			break
		}
		values := assignments[field]

		if err = fieldData.container.Retrieve(values, &pl); err != nil {
			return nil, err
		}
		scanner.MergeFieldResult(field, pl)
		pl.Clear()
	}

	ReleasePostingList(pl)

	return scanner.results.ToArray(), nil
}
