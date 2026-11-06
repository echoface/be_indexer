package segmentindex

import (
	"context"
	"errors"
	"reflect"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/planner"
)

func equalIDs(got, want []be.DocID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func mustAssign(t *testing.T, store *Store, q be.Assignments) []be.DocID {
	t.Helper()
	got, err := store.RetrieveAssignments(q)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStoreAddFlushRetrieve(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("ad"))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")).In("kind", be.NewStrValues("organic"))),
		be.NewDocument(3).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("shanghai")).In("kind", be.NewStrValues("ad"))),
	}
	if err := store.AddDocuments(docs...); err != nil {
		t.Fatal(err)
	}
	// buffered docs stay invisible until a flush seals them
	got := mustAssign(t, store, be.Assignments{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("ad")})
	if !equalIDs(got, nil) {
		t.Fatalf("pending docs visible: %v", got)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		q    be.Assignments
		want []be.DocID
	}{
		{"city+kind hit", be.Assignments{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("ad")}, []be.DocID{1}},
		{"same city different kind", be.Assignments{"city": be.NewStrValues("beijing"), "kind": be.NewStrValues("organic")}, []be.DocID{2}},
		{"city miss", be.Assignments{"city": be.NewStrValues("guangzhou"), "kind": be.NewStrValues("ad")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustAssign(t, store, tc.q)
			if !equalIDs(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	stats := store.Stats()
	if stats.Deltas != 1 || stats.Groups != 3 || stats.PendingDocs != 0 {
		t.Fatalf("stats = %+v, want 1 delta / 3 groups / 0 pending", stats)
	}
	// the exact-term planner path works across deltas too
	ids, err := store.Retrieve(planner.Query{Terms: []planner.TermQuery{
		{Field: "city", Value: "shanghai", Op: planner.OpAND},
		{Field: "kind", Value: "ad", Op: planner.OpAND},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(ids, []be.DocID{3}) {
		t.Fatalf("retrieve got %v want [3]", ids)
	}
}

func TestStoreDeleteTombstone(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	beijing := be.Assignments{"city": be.NewStrValues("beijing")}
	shanghai := be.Assignments{"city": be.NewStrValues("shanghai")}
	if err := store.AddDocuments(
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing"))),
		be.NewDocument(2).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("shanghai"))),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(1); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, beijing); !equalIDs(got, nil) {
		t.Fatalf("deleted doc still matches: %v", got)
	}
	if got := mustAssign(t, store, shanghai); !equalIDs(got, []be.DocID{2}) {
		t.Fatalf("untouched doc lost: %v", got)
	}
	// deleting an unknown id is a no-op
	if err := store.Delete(999); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, shanghai); !equalIDs(got, []be.DocID{2}) {
		t.Fatalf("unknown delete changed results: %v", got)
	}
	// one delete kills every version: add v2 on top of the flushed v1
	if err := store.Update(be.NewDocument(1).AddConjunction(be.NewConjunction().In("city", be.NewStrValues("beijing")))); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, beijing); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("updated version not visible: %v", got)
	}
	if err := store.Delete(1); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, beijing); !equalIDs(got, nil) {
		t.Fatalf("delete kept some version alive: %v", got)
	}
}

func TestStoreUpdate(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	ad := be.Assignments{"kind": be.NewStrValues("ad")}
	organic := be.Assignments{"kind": be.NewStrValues("organic")}
	if err := store.AddDocuments(
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad"))),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("organic")))); err != nil {
		t.Fatal(err)
	}
	// the old version dies immediately; the new one waits for the next flush
	if got := mustAssign(t, store, ad); !equalIDs(got, nil) {
		t.Fatalf("old version still matches: %v", got)
	}
	if got := mustAssign(t, store, organic); !equalIDs(got, nil) {
		t.Fatalf("new version visible before flush: %v", got)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, ad); !equalIDs(got, nil) {
		t.Fatalf("old version resurrected after flush: %v", got)
	}
	if got := mustAssign(t, store, organic); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("new version missing after flush: %v", got)
	}
}

func TestStoreDeleteThenReAdd(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	old := be.Assignments{"kind": be.NewStrValues("ad")}
	fresh := be.Assignments{"kind": be.NewStrValues("organic")}
	if err := store.AddDocuments(
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad"))),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(1); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, old); !equalIDs(got, nil) {
		t.Fatalf("deleted doc matches: %v", got)
	}
	if err := store.AddDocuments(
		be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("organic"))),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, old); !equalIDs(got, nil) {
		t.Fatalf("old terms resurrected: %v", got)
	}
	if got := mustAssign(t, store, fresh); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("re-added doc missing: %v", got)
	}
}

func TestStoreAddDocumentsAtomic(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	good := be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad")))
	bad := be.NewDocument(2).AddConjunction(be.NewConjunction().AddBoolExprs(
		be.NewBoolExpr2("age", be.NewBoolValue(be.ValueOpt(99), int64(1), true)),
	))
	if err := store.AddDocuments(good, bad); !errors.Is(err, ErrUnsupportedExpression) {
		t.Fatalf("error = %v, want ErrUnsupportedExpression", err)
	}
	// the failed batch is rolled back completely: nothing pending, nothing found
	if stats := store.Stats(); stats.PendingDocs != 0 {
		t.Fatalf("rollback left %d pending docs", stats.PendingDocs)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("ad")}); !equalIDs(got, nil) {
		t.Fatalf("rolled back doc visible: %v", got)
	}
	// the builder is still usable after rollback
	if err := store.AddDocuments(good); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("ad")}); !equalIDs(got, []be.DocID{1}) {
		t.Fatalf("got %v want [1]", got)
	}
}

func TestStoreSnapshotStableAcrossWrites(t *testing.T) {
	store := NewStore(StoreOptions{})
	defer store.Close()
	mk := func(id be.DocID) *be.Document {
		return be.NewDocument(id).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad")))
	}
	if err := store.AddDocuments(mk(1), mk(2), mk(3)); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// everything a store can do behind the snapshot's back
	if err := store.Delete(2); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDocuments(mk(4)); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := store.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	q := be.Assignments{"kind": be.NewStrValues("ad")}
	got, err := snapshot.RetrieveAssignments(q)
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(got, []be.DocID{1, 2, 3}) {
		t.Fatalf("snapshot drifted: %v", got)
	}
	if got := mustAssign(t, store, q); !equalIDs(got, []be.DocID{1, 3, 4}) {
		t.Fatalf("store view wrong: %v", got)
	}
	snapshot.Close()
	if _, err := snapshot.RetrieveAssignments(q); !errors.Is(err, ErrSnapshotDone) {
		t.Fatalf("closed snapshot error = %v, want ErrSnapshotDone", err)
	}
}

func TestStoreClosedRejectsOperations(t *testing.T) {
	store := NewStore(StoreOptions{})
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	doc := be.NewDocument(1).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad")))
	if err := store.AddDocuments(doc); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("AddDocuments error = %v, want ErrStoreClosed", err)
	}
	if err := store.Delete(1); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Delete error = %v, want ErrStoreClosed", err)
	}
	if err := store.Update(doc); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Update error = %v, want ErrStoreClosed", err)
	}
	if err := store.Flush(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Flush error = %v, want ErrStoreClosed", err)
	}
	if err := store.Compact(context.Background()); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Compact error = %v, want ErrStoreClosed", err)
	}
	if _, err := store.Snapshot(); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("Snapshot error = %v, want ErrStoreClosed", err)
	}
}

func TestStoreFlushDocsThreshold(t *testing.T) {
	store := NewStore(StoreOptions{FlushDocs: 2})
	defer store.Close()
	mk := func(id be.DocID) *be.Document {
		return be.NewDocument(id).AddConjunction(be.NewConjunction().In("kind", be.NewStrValues("ad")))
	}
	if err := store.AddDocuments(mk(1)); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Deltas != 0 {
		t.Fatalf("early auto flush: %+v", stats)
	}
	if err := store.AddDocuments(mk(2)); err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Deltas != 1 || stats.PendingDocs != 0 {
		t.Fatalf("auto flush missing: %+v", stats)
	}
	if got := mustAssign(t, store, be.Assignments{"kind": be.NewStrValues("ad")}); !reflect.DeepEqual(got, []be.DocID{1, 2}) {
		t.Fatalf("got %v want [1 2]", got)
	}
}
