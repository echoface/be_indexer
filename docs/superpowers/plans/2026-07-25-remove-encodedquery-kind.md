# Remove `EncodedQuery.Kind` — Decouple Encoder from Container Routing

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the `Kind` field from `EncodedQuery` so that `PredicateEncoder` only handles value encoding, while container routing is determined entirely by `FieldMeta.Container` in the schema.

**Architecture:** Currently `EncodedQuery.Kind` is hardcoded by each encoder (e.g. `QueryKindTerm`, `"ext_range"`, `"ac_matcher"`) and used by `engine.initCursors` as a routing discriminator. This couples the encoder to the container. After this refactoring, the engine determines routing solely from `FieldMeta.Container` — the encoder returns only encoded values. The `QueryKind` type and `QueryKindTerm` constant are removed. The `initCursors` switch is simplified to a single schema-driven branch.

**Tech Stack:** Go 1.24, GoConvey for tests.

## Global Constraints

- No new go.mod dependencies.
- Tests use GoConvey (`github.com/smartystreets/goconvey/convey`), test packages named `<pkg>_test`.
- Commands: test = `make test`, vet = `go vet ./...`.
- No panics reachable from the retrieve path.
- `DocID` range, K < 256 limits unchanged.

## Files Affected

| File | Action | Summary |
|------|--------|---------|
| `parser/encoder.go` | Modify | Remove `QueryKind` type, `QueryKindTerm` const, `Kind` field from `EncodedQuery`; update `ExactTermEncoder.Query`, `RangeEncoder.Query`, `ACEncoder.Query` to drop `Kind` |
| `parser/encoder_test.go` | Modify | Remove `Kind` assertions from all 3 tests |
| `engine/searcher.go` | Modify | Simplify `initCursors`: remove `switch q.Kind`, replace with schema-driven single branch |
| `container/geo/encoder.go` | Modify | Remove `Kind` from `EncodedQuery` construction in `Query()` |
| `container/geo/geo_test.go` | Modify | Remove `q.Kind` assertion |
| `container/example/example.go` | Modify | Remove `Kind` from `EncodedQuery` construction in `Query()`, update doc comments |

---

### Task 1: Remove `Kind` from `EncodedQuery` struct and built-in encoders

**Files:**
- Modify: `parser/encoder.go:12-18,27-33,184-195,216-222,248-257`
- Modify: `parser/encoder_test.go:28,56,80`

**Interfaces:**
- Consumes: `core.ValueExpr`, `ValueTokenizer`, `ParseRangePoint`, `ParseRangeExpr`, `ValuesToStrings`
- Produces: `EncodedQuery` (without `Kind`), `PredicateEncoder` interface (unchanged signature)

- [ ] **Step 1: Remove `QueryKind` type, `QueryKindTerm` constant, and `Kind` field from `EncodedQuery`**

In `parser/encoder.go`, delete lines 12-18 (`QueryKind` type and `QueryKindTerm` const) and remove the `Kind` field from the `EncodedQuery` struct:

```go
// EncodedQuery is the query-side physical representation of one assignment.
// Value is container-defined and passed directly to ContainerReader.Retrieve.
type EncodedQuery struct {
	Value any
}
```

- [ ] **Step 2: Update `ExactTermEncoder.Query` to drop `Kind`**

In `parser/encoder.go`, change `ExactTermEncoder.Query` (line 184-195):

```go
func (e ExactTermEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	terms, err := e.tokenizer.TokenizeAssign(value)
	if err != nil {
		return nil, err
	}
	terms = util.DistinctString(terms)
	out := make([]EncodedQuery, 0, len(terms))
	for _, term := range terms {
		out = append(out, EncodedQuery{Value: term})
	}
	return out, nil
}
```

- [ ] **Step 3: Update `RangeEncoder.Query` to drop `Kind`**

In `parser/encoder.go`, change `RangeEncoder.Query` (line 216-222):

```go
func (RangeEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	point, err := ParseRangePoint(value)
	if err != nil {
		return nil, err
	}
	return []EncodedQuery{{Value: point}}, nil
}
```

- [ ] **Step 4: Update `ACEncoder.Query` to drop `Kind`**

In `parser/encoder.go`, change `ACEncoder.Query` (line 248-257):

```go
func (ACEncoder) Query(value interface{}) ([]EncodedQuery, error) {
	parts, err := ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, nil
	}
	return []EncodedQuery{{Value: strings.Join(parts, " ")}}, nil
}
```

- [ ] **Step 5: Update encoder tests to remove `Kind` assertions**

In `parser/encoder_test.go`, update the three test functions:

`TestExactTermEncoderUsesTokenizerInBothDirections` (line 28):
```go
	if len(queries) != 1 || queries[0].Value != "18" {
		t.Fatalf("unexpected query keys: %#v", queries)
	}
```

`TestRangeEncoderBuildAndQuery` (line 56):
```go
	if len(queries) != 1 || queries[0].Value != int64(25) {
		t.Fatalf("unexpected range query: %#v", queries)
	}
```

`TestACEncoderBuildAndQuery` (line 80):
```go
	if len(queries) != 1 || queries[0].Value != "I love apple" {
		t.Fatalf("unexpected ac query: %#v", queries)
	}
```

- [ ] **Step 6: Run parser tests to verify**

Run: `go test ./parser/ -v`
Expected: PASS — all 3 tests pass

- [ ] **Step 7: Commit**

```bash
git add parser/encoder.go parser/encoder_test.go
git commit -m "refactor(parser): remove Kind from EncodedQuery, decouple encoder from container routing"
```

---

### Task 2: Simplify engine `initCursors` routing

**Files:**
- Modify: `engine/searcher.go:224-248`

**Interfaces:**
- Consumes: `EncodedQuery` (from Task 1, no `Kind`), `FieldMeta.Container` from `schemaCodec`, `SegmentReader.GetPostingsByTerm`, `SegmentReader.ContainerQuery`
- Produces: `*core.FieldCursors` (unchanged)

- [ ] **Step 1: Rewrite the query dispatch loop in `initCursors`**

In `engine/searcher.go`, replace the `switch q.Kind` block (lines 224-248) with a schema-driven single branch:

```go
		var iterators []core.PostingIterator
		for _, seg := range e.segments {
			for _, q := range ef.queries {
				if containerName != "" {
					iters, err := seg.ContainerQuery(field, containerName, q.Value)
					if err == nil && len(iters) > 0 {
						iterators = append(iterators, iters...)
					}
				} else {
					term, ok := q.Value.(string)
					if !ok {
						continue
					}
					it, err := seg.GetPostingsByTerm(field, term)
					if err == nil && it != nil {
						iterators = append(iterators, it)
					}
				}
			}
		}
```

- [ ] **Step 2: Update the comment above `containerName` to reflect the new design**

Replace the comment block at lines 212-221:

```go
		// Determine the field's container type once per field.
		// Routing is driven entirely by the schema: a non-default Container
		// sends all query values through ContainerQuery; the default path
		// uses FlatDict for string term lookups.
		containerName := ""
		if fc, ok := e.schemaCodec.Field(field); ok {
			c := fc.Meta.Container
			if c != "" && c != core.IndexNameDefault && segment.HasContainer(c) {
				containerName = c
			}
		}
```

- [ ] **Step 3: Run engine tests**

Run: `go test ./engine/ -v`
Expected: PASS

- [ ] **Step 4: Run full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add engine/searcher.go
git commit -m "refactor(engine): simplify initCursors routing, remove QueryKind switch"
```

---

### Task 3: Update external encoders (geo, example)

**Files:**
- Modify: `container/geo/encoder.go:129-143`
- Modify: `container/geo/geo_test.go:133`
- Modify: `container/example/example.go:6,83-93`

**Interfaces:**
- Consumes: `parser.EncodedQuery` (from Task 1, no `Kind`)
- Produces: updated `geo.Encoder.Query()`, `example.Encoder.Query()`

- [ ] **Step 1: Update `geo.Encoder.Query` to drop `Kind`**

In `container/geo/encoder.go`, change the `Query` method (lines 129-143):

```go
func (e Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	q, ok := value.(GeoQuery)
	if !ok {
		return nil, fmt.Errorf("proximitygeo: expected geo.GeoQuery, got %T", value)
	}
	if err := validateLatLng(q.Lat, q.Lng); err != nil {
		return nil, err
	}

	gh := geohash.EncodeWithPrecision(q.Lat, q.Lng, maxQueryPrecision)
	out := make([]parser.EncodedQuery, 0, maxQueryPrecision-minPrecision+1)
	for p := minPrecision; p <= len(gh); p++ {
		out = append(out, parser.EncodedQuery{Value: gh[:p]})
	}
	return out, nil
}
```

- [ ] **Step 2: Update `geo_test.go` to remove `Kind` assertion**

In `container/geo/geo_test.go`, update `TestEncoderQuery` (line 132-134):

```go
		for i, q := range out {
			convey.So(q.Value, convey.ShouldEqual, full[:3+i])
		}
```

- [ ] **Step 3: Update `example.Encoder.Query` to drop `Kind`**

In `container/example/example.go`, change the `Query` method (lines 83-93):

```go
func (Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	texts, err := parser.ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	out := make([]parser.EncodedQuery, 0, len(texts))
	for _, s := range texts {
		out = append(out, parser.EncodedQuery{Value: s})
	}
	return out, nil
}
```

- [ ] **Step 4: Update `example/example.go` doc comments**

In `container/example/example.go`, update the package doc (lines 1-15) to remove references to `Kind`:

```go
// Package example is a minimal, end-to-end template showing how library
// users plug a custom index container + predicate encoder into be_indexer
// WITHOUT touching engine/segment/builder code:
//
//  1. Implement parser.PredicateEncoder: translate business predicates into
//     EncodedPosting (build) and EncodedQuery (query).
//  2. Implement segment.ContainerBuilder/ContainerReader: serialize the
//     per-field term→PostingRef table into a byte block and answer queries
//     against it with zero-copy posting cursors.
//  3. Register both in init() under the same name; users select it via
//     FieldMeta.FieldOption{Container: ContainerName}.
```

Also update the data flow comment (line 14-15) to remove "default Kind branch":

```go
//	Build:  doc_exporter → sink.AddRecord(field, container, record, entries)
//	Query:  engine → encoder.Query(value) → []EncodedQuery → ContainerQuery
```

- [ ] **Step 5: Run tests for geo and example packages**

Run: `go test ./container/... -v`
Expected: PASS

- [ ] **Step 6: Run full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add container/geo/encoder.go container/geo/geo_test.go container/example/example.go
git commit -m "refactor(container): remove Kind from external encoder Query outputs"
```

---

### Task 4: Update remaining doc comments referencing `QueryKind`

**Files:**
- Modify: `container/mph/mph.go:5-7` (doc comment)
- Modify: `parser/encoder.go:27-33` (struct comment already updated in Task 1, verify)

**Interfaces:**
- Consumes: N/A (doc-only changes)
- Produces: consistent documentation

- [ ] **Step 1: Update `mph.go` doc comment**

In `container/mph/mph.go`, update lines 5-7:

```go
// No custom encoder is needed — the default ExactTermEncoder handles value
// encoding, and the engine routes queries through the mph container when
// the field's Container is "mph_dict".
```

- [ ] **Step 2: Verify no remaining references to `QueryKind` or `Kind` in `EncodedQuery` context**

Run: `rg "QueryKind|q\.Kind" --type go`
Expected: No matches (or only unrelated uses)

- [ ] **Step 3: Run full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 4: Run vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 5: Commit**

```bash
git add container/mph/mph.go
git commit -m "docs: update comments to reflect QueryKind removal"
```

---

### Task 5: Final verification

- [ ] **Step 1: Run full test suite**

Run: `make test`
Expected: PASS — all tests pass

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Verify `QueryKind` is fully removed**

Run: `rg "QueryKind" --type go`
Expected: No matches

Run: `rg "EncodedQuery\{.*Kind" --type go`
Expected: No matches

- [ ] **Step 4: Verify diff is minimal and correct**

Run: `git diff --stat`
Expected: Only the 6 files listed in the Files Affected table are changed.

Run: `git log --oneline -5`
Expected: 4-5 new commits from this refactoring.
