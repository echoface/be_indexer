package planner

import (
	"encoding/json"

	"github.com/echoface/be_indexer/storage"
)

// ExplainResult is a stable, machine-readable description of planning.
type ExplainResult struct {
	FieldCount     int        `json:"field_count"`
	TermCount      int        `json:"term_count"`
	SkippedSegment bool       `json:"skipped_segment"`
	EstimatedHits  uint64     `json:"estimated_hits"`
	EstimatedCost  uint64     `json:"estimated_cost"`
	Terms          []TermPlan `json:"terms"`
}

func Explain(segment *storage.Segment, plan Plan) ExplainResult {
	result := ExplainResult{
		SkippedSegment: plan.SkippedSegment,
		EstimatedHits:  plan.EstimatedHits,
		EstimatedCost:  plan.EstimatedCost,
		Terms:          append([]TermPlan(nil), plan.Terms...),
	}
	if segment != nil {
		result.FieldCount = len(segment.Fields())
		result.TermCount = segment.TermCount()
	}
	return result
}

func (e ExplainResult) JSON() ([]byte, error) {
	return json.MarshalIndent(e, "", "  ")
}
