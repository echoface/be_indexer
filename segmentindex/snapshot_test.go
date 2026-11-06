package segmentindex

import (
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
)

func segmentBuild(t *testing.T, manager *SnapshotManager, id be.DocID, value string) *Snapshot {
	t.Helper()
	builder, err := manager.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.AddDocuments(be.NewDocument(id).AddConjunction(
		be.NewConjunction().In("city", be.NewStrValues(value)),
	)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Publish(builder)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSnapshotBuildIsolatedAndPublishedAtomically(t *testing.T) {
	manager := NewSnapshotManager()
	defer manager.Close()
	old := segmentBuild(t, manager, 1, "beijing")
	defer old.Close()

	builder, err := manager.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.AddDocuments(be.NewDocument(2).AddConjunction(
		be.NewConjunction().In("city", be.NewStrValues("shanghai")),
	)); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	got, err := current.Retrieve(planner.Query{Terms: []planner.TermQuery{{Field: "city", Value: "shanghai", Op: planner.OpAND}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unpublished result visible: %v", got)
	}
	current.Close()

	newSnapshot, err := manager.Publish(builder)
	if err != nil {
		t.Fatal(err)
	}
	defer newSnapshot.Close()
	if !old.Retired() || newSnapshot.Version() != 2 {
		t.Fatalf("old retired=%v new version=%d", old.Retired(), newSnapshot.Version())
	}
	got, err = newSnapshot.Retrieve(planner.Query{Terms: []planner.TermQuery{{Field: "city", Value: "shanghai", Op: planner.OpAND}}})
	if err != nil || !reflect.DeepEqual(got, []be.DocID{2}) {
		t.Fatalf("published result=%v err=%v", got, err)
	}
	got, err = old.Retrieve(planner.Query{Terms: []planner.TermQuery{{Field: "city", Value: "beijing", Op: planner.OpAND}}})
	if err != nil || !reflect.DeepEqual(got, []be.DocID{1}) {
		t.Fatalf("old snapshot result=%v err=%v", got, err)
	}
}
