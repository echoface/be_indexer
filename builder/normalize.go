package builder

import (
	"fmt"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

// validateCodecContainers rejects a schema whose IndexType does not resolve to a
// registered container (e.g. a missing side-effect import). parser validates
// encoders; this closes the IndexType gap at build construction time so the
// error surfaces before any document is processed rather than as a silent
// default-container fallback at serving time.
func validateCodecContainers(codec *parser.SchemaCodec) error {
	fields := codec.Fields()
	options := make([]core.SchemaField, 0, len(fields))
	for _, fc := range fields {
		options = append(options, core.SchemaField{Field: fc.Field, Option: fc.Option})
	}
	return segment.ValidateFieldOptions(options)
}

// NormalizeOptions controls schema-aware document validation and normalization.
type NormalizeOptions struct {
	// IgnoreUnindexedFields controls what happens when a document references a
	// field that is not present in the schema.
	//
	//   false (default): the build fails. This is the safe choice: an unindexed
	//   Include field would still be counted into K yet produce no posting,
	//   making the conjunction impossible to satisfy (false negative); an
	//   unindexed Exclude field would be silently dropped (false positive).
	//
	//   true: unindexed fields are dropped from a normalized copy of the
	//   conjunction *before* K is computed, so K reflects only schema-known
	//   Include fields. This tolerates extra fields at the cost of ignoring their
	//   logical effect.
	IgnoreUnindexedFields bool
}

// ValidateAndNormalizeDocument is the single schema-aware guard every build path
// runs before a document is encoded into a segment. It:
//
//   - validates the DocID range and conjunction-count limit;
//   - rejects nil conjunctions and nil constraints;
//   - rejects a field carrying more than one Include constraint in the same
//     conjunction (ambiguous AND-vs-OR semantics: FieldCursor merges same-field
//     postings as OR while K counts the field once — see design note §4.1.6).
//     Multiple Exclude constraints on a field are allowed (any hit vetoes);
//   - handles unindexed fields per NormalizeOptions.
//
// It returns the document to encode. When no normalization is required (the
// common case: all fields indexed, no multi-include), it returns the input
// document unchanged with zero extra allocation. Otherwise it returns a shallow
// copy whose affected conjunctions have been rewritten; the caller's document is
// never mutated.
//
// Crucially, K must be computed from the *returned* document so that dropped
// unindexed fields do not inflate K.
func ValidateAndNormalizeDocument(codec *parser.SchemaCodec, doc *core.Document, opt NormalizeOptions) (*core.Document, error) {
	if doc == nil {
		return nil, fmt.Errorf("nil document")
	}
	if !core.ValidDocID(doc.ID) {
		return nil, fmt.Errorf("invalid doc id %d: exceeds 43-bit encoding range", doc.ID)
	}
	if len(doc.Cons) >= 256 {
		return nil, fmt.Errorf("doc %d has too many conjunctions: %d >= 256", doc.ID, len(doc.Cons))
	}

	docChanged := false
	normCons := doc.Cons // shared until a conjunction actually needs rewriting

	for conjIdx, conj := range doc.Cons {
		if conj == nil {
			return nil, fmt.Errorf("doc %d conjunction %d is nil", doc.ID, conjIdx)
		}

		var normPredicates map[core.BEField][]*core.ValueExpr // non-nil once this conj is rewritten
		for field, exprs := range conj.Predicates {
			includeCount := 0
			for _, e := range exprs {
				if e == nil {
					return nil, fmt.Errorf("doc %d conjunction %d field %q has a nil constraint", doc.ID, conjIdx, field)
				}
				if e.Incl {
					includeCount++
				}
			}

			if _, ok := codec.Field(field); !ok {
				if !opt.IgnoreUnindexedFields {
					return nil, fmt.Errorf("doc %d conjunction %d references unindexed field %q; add it to the schema or set IgnoreUnindexedFields", doc.ID, conjIdx, field)
				}
				// Drop the unindexed field from a normalized copy so it neither
				// inflates K (Include) nor is silently ignored without record
				// (Exclude): here dropping is the explicit, opt-in behavior.
				if normPredicates == nil {
					normPredicates = clonePredicates(conj.Predicates)
				}
				delete(normPredicates, field)
				continue
			}

			// §4.1.6: at most one Include constraint per field per conjunction.
			if includeCount > 1 {
				return nil, fmt.Errorf(
					"doc %d conjunction %d field %q has %d include constraints; a field may carry at most one include "+
						"(merge a range intersection into a single Between, or put multiple equality values into one constraint)",
					doc.ID, conjIdx, field, includeCount)
			}
		}

		if normPredicates != nil {
			if !docChanged {
				normCons = append([]*core.Conjunction(nil), doc.Cons...)
				docChanged = true
			}
			normCons[conjIdx] = &core.Conjunction{Predicates: normPredicates}
		}
	}

	if !docChanged {
		return doc, nil
	}
	return &core.Document{ID: doc.ID, Version: doc.Version, Cons: normCons}, nil
}

// clonePredicates shallow-copies the predicate map. The []*ValueExpr slices and
// their elements are treated as immutable during a build, so they are shared.
func clonePredicates(src map[core.BEField][]*core.ValueExpr) map[core.BEField][]*core.ValueExpr {
	dst := make(map[core.BEField][]*core.ValueExpr, len(src))
	for f, exprs := range src {
		dst[f] = exprs
	}
	return dst
}
