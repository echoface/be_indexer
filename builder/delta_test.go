package builder_test

import (
	"testing"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
)

func TestBuildDeltaPlanKeepsLatestMutation(t *testing.T) {
	oldDoc := core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 1))
	newDoc := core.NewDocument(1).AddConjunction(core.NewConjunction().In("a", 2))
	plan, err := builder.BuildDeltaPlan([]builder.Mutation{
		{DocID: 1, Version: 1, Op: builder.MutationUpsert, Document: oldDoc},
		{DocID: 1, Version: 2, Op: builder.MutationUpsert, Document: newDoc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ChangedDocs) != 1 || plan.ChangedDocs[0] != 1 {
		t.Fatalf("unexpected changed docs: %v", plan.ChangedDocs)
	}
	if len(plan.DeletedDocs) != 0 {
		t.Fatalf("unexpected deleted docs: %v", plan.DeletedDocs)
	}
	if len(plan.Documents) != 1 || plan.Documents[0].Version != 2 {
		t.Fatalf("unexpected delta docs: %+v", plan.Documents)
	}
}

func TestBuildDeltaPlanDeleteWinsLatest(t *testing.T) {
	doc := core.NewDocument(2).AddConjunction(core.NewConjunction().In("a", 1))
	plan, err := builder.BuildDeltaPlan([]builder.Mutation{
		{DocID: 2, Version: 1, Op: builder.MutationUpsert, Document: doc},
		{DocID: 2, Version: 3, Op: builder.MutationDelete},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Documents) != 0 {
		t.Fatalf("delete latest should not emit docs: %+v", plan.Documents)
	}
	if len(plan.ChangedDocs) != 1 || plan.ChangedDocs[0] != 2 {
		t.Fatalf("unexpected changed docs: %v", plan.ChangedDocs)
	}
	if len(plan.DeletedDocs) != 1 || plan.DeletedDocs[0] != 2 {
		t.Fatalf("unexpected deleted docs: %v", plan.DeletedDocs)
	}
}

func TestBuildDeltaPlanRecreateAfterDelete(t *testing.T) {
	doc := core.NewDocument(3).AddConjunction(core.NewConjunction().In("a", 7))
	plan, err := builder.BuildDeltaPlan([]builder.Mutation{
		{DocID: 3, Version: 4, Op: builder.MutationDelete},
		{DocID: 3, Version: 5, Op: builder.MutationUpsert, Document: doc},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Documents) != 1 || plan.Documents[0].ID != 3 || plan.Documents[0].Version != 5 {
		t.Fatalf("unexpected docs: %+v", plan.Documents)
	}
	if len(plan.DeletedDocs) != 0 {
		t.Fatalf("recreate should clear final delete: %v", plan.DeletedDocs)
	}
}

func TestBuildDeltaPlanRejectsInvalidUpsert(t *testing.T) {
	if _, err := builder.BuildDeltaPlan([]builder.Mutation{{DocID: 1, Version: 1, Op: builder.MutationUpsert}}); err == nil {
		t.Fatal("expected nil document error")
	}
	if _, err := builder.BuildDeltaPlan([]builder.Mutation{{DocID: 1, Version: 1, Op: builder.MutationUpsert, Document: core.NewDocument(2)}}); err == nil {
		t.Fatal("expected doc id mismatch error")
	}
}
