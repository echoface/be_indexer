package builder

import (
	"fmt"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// MutationOp describes the final operation for a changed document.
type MutationOp uint8

const (
	MutationUpsert MutationOp = iota + 1
	MutationDelete
)

// Mutation is an input event for delta index construction.
// Version must be monotonically increasing for the same DocID.
type Mutation struct {
	DocID    core.DocID
	Version  uint64
	Op       MutationOp
	Document *core.Document
}

// DeltaPlan is the normalized latest-state view used to build a delta index.
type DeltaPlan struct {
	Documents   []*core.Document
	ChangedDocs []core.DocID
	DeletedDocs []core.DocID
}

// validateMutation checks a single mutation's structural validity. It is the
// per-mutation (isolatable) validation used both by DeltaIndexBuilder.AddMutation
// (enqueue-time, subject to FailMode) and by BuildDeltaPlan (defensive).
func validateMutation(mutation Mutation) error {
	if !core.ValidDocID(mutation.DocID) {
		return fmt.Errorf("invalid mutation doc id %d", mutation.DocID)
	}
	if mutation.Op != MutationUpsert && mutation.Op != MutationDelete {
		return fmt.Errorf("invalid mutation op %d for doc %d", mutation.Op, mutation.DocID)
	}
	if mutation.Op == MutationUpsert {
		if mutation.Document == nil {
			return fmt.Errorf("upsert mutation for doc %d requires document", mutation.DocID)
		}
		if mutation.Document.ID != mutation.DocID {
			return fmt.Errorf("upsert mutation doc id mismatch: event=%d document=%d", mutation.DocID, mutation.Document.ID)
		}
	}
	return nil
}

// BuildDeltaPlan compacts mutations by DocID and keeps only the highest version.
// Upserts are emitted as delta documents; deletes are represented only in the
// changed/deleted sidecars.
func BuildDeltaPlan(mutations []Mutation) (*DeltaPlan, error) {
	latest := make(map[core.DocID]Mutation, len(mutations))
	for _, mutation := range mutations {
		if err := validateMutation(mutation); err != nil {
			return nil, err
		}
		prev, ok := latest[mutation.DocID]
		if !ok || mutation.Version > prev.Version || (mutation.Version == prev.Version && mutation.Op == MutationDelete) {
			latest[mutation.DocID] = mutation
		}
	}

	changed := make([]core.DocID, 0, len(latest))
	for id := range latest {
		changed = append(changed, id)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })

	plan := &DeltaPlan{ChangedDocs: changed}
	for _, id := range changed {
		mutation := latest[id]
		switch mutation.Op {
		case MutationUpsert:
			mutation.Document.Version = mutation.Version
			plan.Documents = append(plan.Documents, mutation.Document)
		case MutationDelete:
			plan.DeletedDocs = append(plan.DeletedDocs, id)
		}
	}
	return plan, nil
}
