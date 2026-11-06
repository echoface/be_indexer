package be_indexer

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func snapshotTestBuilder(t *testing.T, docID DocID, value int) *SnapshotBuilder {
	t.Helper()
	builder, err := NewSnapshotManager().BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	builder.ConfigField("A", FieldOption{Container: HolderNameDefault})
	if err := builder.AddDocument(NewDocument(docID).AddConjunction(
		NewConjunction().In("A", NewIntValues(value)),
	)); err != nil {
		t.Fatal(err)
	}
	return builder
}

func snapshotTestBuild(t *testing.T, manager *SnapshotManager, docID DocID, value int) *Snapshot {
	t.Helper()
	builder, err := manager.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	builder.ConfigField("A", FieldOption{Container: HolderNameDefault})
	if err := builder.AddDocument(NewDocument(docID).AddConjunction(
		NewConjunction().In("A", NewIntValues(value)),
	)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Publish(builder)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func snapshotIDs(t *testing.T, snapshot *Snapshot, value int) DocIDList {
	t.Helper()
	ids, err := snapshot.Retrieve(Assignments{"A": NewIntValues(value)})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestSnapshotManager_PublishAndRetainOldSnapshot(t *testing.T) {
	manager := NewSnapshotManager()
	defer manager.Close()

	old := snapshotTestBuild(t, manager, 1, 1)
	if old.Version() != 1 {
		t.Fatalf("old version = %d, want 1", old.Version())
	}
	if got := snapshotIDs(t, old, 1); !reflect.DeepEqual(got, DocIDList{1}) {
		t.Fatalf("old result = %v, want [1]", got)
	}

	newSnapshot := snapshotTestBuild(t, manager, 2, 2)
	defer newSnapshot.Close()
	if newSnapshot.Version() != 2 {
		t.Fatalf("new version = %d, want 2", newSnapshot.Version())
	}
	if !old.Retired() {
		t.Fatal("old snapshot should be retired after publish")
	}
	if got := snapshotIDs(t, old, 1); !reflect.DeepEqual(got, DocIDList{1}) {
		t.Fatalf("retained old result = %v, want [1]", got)
	}
	if got := snapshotIDs(t, newSnapshot, 2); !reflect.DeepEqual(got, DocIDList{2}) {
		t.Fatalf("new result = %v, want [2]", got)
	}

	current, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if current.Version() != newSnapshot.Version() {
		t.Fatalf("current version = %d, want %d", current.Version(), newSnapshot.Version())
	}
	old.Close()
}

func TestSnapshotManager_BuildIsolatedUntilPublish(t *testing.T) {
	manager := NewSnapshotManager()
	defer manager.Close()

	initial := snapshotTestBuild(t, manager, 1, 1)
	defer initial.Close()

	builder, err := manager.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	builder.ConfigField("A", FieldOption{Container: HolderNameDefault})
	if err := builder.AddDocument(NewDocument(2).AddConjunction(
		NewConjunction().In("A", NewIntValues(2)),
	)); err != nil {
		t.Fatal(err)
	}

	current, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.Version() != initial.Version() {
		t.Fatalf("current version changed during build: got %d want %d", current.Version(), initial.Version())
	}
	if got := snapshotIDs(t, current, 2); len(got) != 0 {
		t.Fatalf("unpublished document visible in current snapshot: %v", got)
	}
	current.Close()

	published, err := manager.Publish(builder)
	if err != nil {
		t.Fatal(err)
	}
	defer published.Close()
	if got := snapshotIDs(t, published, 2); !reflect.DeepEqual(got, DocIDList{2}) {
		t.Fatalf("published result = %v, want [2]", got)
	}
}

func TestSnapshotManager_BuildCannotBePublishedAfterClose(t *testing.T) {
	manager := NewSnapshotManager()
	defer manager.Close()

	builder, err := manager.BeginBuild()
	if err != nil {
		t.Fatal(err)
	}
	builder.Close()
	if _, err := manager.Publish(builder); !errors.Is(err, ErrBuildClosed) {
		t.Fatalf("publish closed build error = %v, want %v", err, ErrBuildClosed)
	}
	if _, err := manager.Current(); !errors.Is(err, ErrNoCurrentSnapshot) {
		t.Fatalf("current error = %v, want %v", err, ErrNoCurrentSnapshot)
	}
}

func TestSnapshotManager_CloseAndSnapshotCloseAreIdempotent(t *testing.T) {
	manager := NewSnapshotManager()
	snapshot := snapshotTestBuild(t, manager, 1, 1)
	manager.Close()
	manager.Close()

	if _, err := manager.BeginBuild(); !errors.Is(err, ErrSnapshotManagerClosed) {
		t.Fatalf("begin after close error = %v, want %v", err, ErrSnapshotManagerClosed)
	}
	if _, err := manager.Current(); !errors.Is(err, ErrSnapshotManagerClosed) {
		t.Fatalf("current after close error = %v, want %v", err, ErrSnapshotManagerClosed)
	}
	if got, err := snapshot.Retrieve(Assignments{"A": NewIntValues(1)}); err != nil || !reflect.DeepEqual(got, DocIDList{1}) {
		t.Fatalf("caller-held snapshot after manager close = %v, %v; want [1], nil", got, err)
	}
	snapshot.Close()
	snapshot.Close()
	if _, err := snapshot.Retrieve(Assignments{"A": NewIntValues(1)}); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("retrieve after caller close error = %v, want %v", err, ErrSnapshotClosed)
	}
}

func TestSnapshotManager_ConcurrentPublishAndRead(t *testing.T) {
	manager := NewSnapshotManager()
	defer manager.Close()
	first := snapshotTestBuild(t, manager, 1, 1)
	defer first.Close()

	const rounds = 20
	var wg sync.WaitGroup
	errorsCh := make(chan error, rounds)
	for i := 0; i < rounds; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			builder, err := manager.BeginBuild()
			if err != nil {
				errorsCh <- err
				return
			}
			builder.ConfigField("A", FieldOption{Container: HolderNameDefault})
			if err := builder.AddDocument(NewDocument(DocID(i + 2)).AddConjunction(
				NewConjunction().In("A", NewIntValues(i+2)),
			)); err != nil {
				errorsCh <- err
				return
			}
			published, err := manager.Publish(builder)
			if err != nil {
				errorsCh <- err
				return
			}
			published.Close()
		}(i)
	}

	for i := 0; i < rounds*4; i++ {
		current, err := manager.Current()
		if err != nil {
			errorsCh <- err
			break
		}
		if current.Version() == 0 {
			errorsCh <- errors.New("published snapshot has zero version")
		}
		current.Close()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
}
