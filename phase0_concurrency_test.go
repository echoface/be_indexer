package be_indexer_test

import (
	"reflect"
	"sync"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/roaringidx"
)

func TestPhase0_ConcurrentReads(t *testing.T) {
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("A", be.NewIntValues(1)).In("B", be.NewIntValues(2))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("A", be.NewIntValues(1)).NotIn("B", be.NewIntValues(9))),
		be.NewDocument(3).AddConjunction(be.NewConjunction()),
	}
	defaultIndex, roaringIndex := buildPhase0Indexes(t, docs)
	query := be.Assignments{"A": be.NewIntValues(1), "B": be.NewIntValues(2)}
	want := []be.DocID{1, 2, 3}

	const workers = 16
	const queriesPerWorker = 100
	var wg sync.WaitGroup
	errors := make(chan string, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < queriesPerWorker; i++ {
				got, err := defaultIndex.Retrieve(query)
				if err != nil {
					errors <- err.Error()
					return
				}
				if !reflect.DeepEqual(sortedDocIDs(got), want) {
					errors <- "default index returned inconsistent result"
					return
				}
				gotBitmap, err := roaringidx.NewScanner(roaringIndex).Retrieve(query)
				if err != nil {
					errors <- err.Error()
					return
				}
				if !reflect.DeepEqual(roaringDocIDs(gotBitmap), want) {
					errors <- "roaring index returned inconsistent result"
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

func TestPhase0_ErrorRecovery(t *testing.T) {
	t.Run("default builder can continue after rejected document", func(t *testing.T) {
		builder := be.NewIndexerBuilder()
		builder.ConfigField("A", be.FieldOption{Container: be.HolderNameDefault})
		if err := builder.AddDocument(be.NewDocument(1)); err == nil {
			t.Fatal("expected empty document error")
		}
		valid := be.NewDocument(2).AddConjunction(be.NewConjunction().In("A", be.NewIntValues(7)))
		if err := builder.AddDocument(valid); err != nil {
			t.Fatalf("add valid document after error: %v", err)
		}
		got, err := builder.BuildIndex().Retrieve(be.Assignments{"A": be.NewIntValues(7)})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sortedDocIDs(got), []be.DocID{2}) {
			t.Fatalf("got %v want [2]", got)
		}
	})

	t.Run("roaring builder reports an unknown field and remains usable", func(t *testing.T) {
		builder := roaringidx.NewIndexerBuilder()
		if err := builder.ConfigureField("A", roaringidx.FieldSetting{
			Container: roaringidx.ContainerNameDefault,
			Parser:    parser.NewNumberParser(),
		}); err != nil {
			t.Fatal(err)
		}
		bad := be.NewDocument(1).AddConjunction(be.NewConjunction().In("unknown", be.NewIntValues(1)))
		if err := builder.AddDocument(bad); err == nil {
			t.Fatal("expected unknown field error")
		}
		valid := be.NewDocument(2).AddConjunction(be.NewConjunction().In("A", be.NewIntValues(1)))
		if err := builder.AddDocument(valid); err != nil {
			t.Fatalf("add valid document after error: %v", err)
		}
		index, err := builder.BuildIndexer()
		if err != nil {
			t.Fatal(err)
		}
		got, err := roaringidx.NewScanner(index).Retrieve(be.Assignments{"A": be.NewIntValues(1)})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(roaringDocIDs(got), []be.DocID{2}) {
			t.Fatalf("got %v want [2]", got)
		}
	})
}
