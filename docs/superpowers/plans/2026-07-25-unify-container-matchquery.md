# Unify Container Interface: BlockContext + MatchQuery

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the dual-path query routing (engine's dict fast path + IndexQuery) with a single unified `IndexReader.MatchQuery(ctx BlockContext, ...)` interface, where the default dict is a registered container like any other.

**Architecture:** Introduce `BlockContext` to carry field storage data (dict + postings) and rename `Retrieve` → `MatchQuery`. Register `DictIndex` as the default container via `segment.RegisterIndex`. The engine's `initCursors` collapses to one code path: look up the container (including "default"), call `MatchQuery`, done.

**Tech Stack:** Go 1.24, GoConvey for tests.

## Global Constraints

- No new go.mod dependencies.
- Tests use GoConvey (`github.com/smartystreets/goconvey/convey`), test packages named `<pkg>_test`.
- Commands: test = `make test`, vet = `go vet ./...`.
- No panics reachable from the retrieve path.
- `GetPostingsByTerm` is kept as a convenience method delegating to `DictIndex` — tests and simple callers retain the shorthand.

## Files Affected

| File | Action | Summary |
|------|--------|---------|
| `segment/field_index.go` | Modify | Add `BlockContext` struct; rename `Retrieve` → `MatchQuery` in `IndexReader` interface |
| `segment/dict_index.go` | Modify | Update `DictIndex` implementing `IndexReader` for the default FlatDict path |
| `segment/segment_reader.go` | Modify | Register default dict; update `IndexQuery` to pass `BlockContext`; rewrite `GetPostingsByTerm` as thin wrapper |
| `segment/range_index.go` | Modify | Rename `Retrieve` → `MatchQuery`, accept `BlockContext`, use `ctx.Pl` instead of `postingBlock` |
| `segment/ac_index.go` | Modify | Rename `Retrieve` → `MatchQuery`, accept `BlockContext`, use `ctx.Pl` instead of `postingBlock` |
| `engine/searcher.go` | Modify | Remove `if containerName != ""` branch; unify to single `IndexQuery` call |
| `container/mph/reader.go` | Modify | Rename `Retrieve` → `MatchQuery`, accept `BlockContext`, use `ctx.Pl` instead of `postingBlock` |
| `container/example/example.go` | Modify | Rename `Retrieve` → `MatchQuery`, accept `BlockContext`, use `ctx.Pl` instead of `postingBlock`; update doc comments |
| `container/mph/mph_test.go` | Modify | Update all `.Retrieve(` → `.MatchQuery(` calls |
| `container/example/example_test.go` | Modify | Update all `.Retrieve(` → `.MatchQuery(` calls |
| `segment/segment_test.go` | Modify | Update direct `GetPostingsByTerm` tests (kept as-is, no change needed) |

---

### Task 1: Define `BlockContext` and update `IndexReader` interface

**Files:**
- Modify: `segment/field_index.go`

**Interfaces:**
- Consumes: `FlatDict` (from `segment/flatmap.go`)
- Produces: `BlockContext` struct, updated `IndexReader` interface with `MatchQuery` method

- [ ] **Step 1: Add `BlockContext` and rename `Retrieve` → `MatchQuery` in `segment/field_index.go`**

Replace the `IndexReader` interface and add `BlockContext` above it:

```go
// BlockContext carries the field-level storage data that containers need to
// resolve posting references. Dict is non-nil only for fields that use the
// default FlatDict path; custom containers ignore it. Pl is the shared
// posting block byte slice used by all containers to create zero-copy
// PostingCursor views.
type BlockContext struct {
	Dict *FlatDict
	Pl   []byte
}

// IndexReader reads a serialized index block and provides query access.
// Instances are created during SegmentReader construction (cold path, once per
// block) and queried during retrieval (hot path). Implementations must be safe
// for concurrent MatchQuery calls.
//
// ctx carries field-level storage (dict + posting block) so that containers can
// resolve posting references without the engine orchestrating multi-step lookups.
type IndexReader interface {
	MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error)
}
```

Also update `IndexReaderFactory` return type — it stays the same since the type name `IndexReader` is unchanged, only the method name changed.

- [ ] **Step 2: Verify build compiles**

Run: `go build ./...`
Expected: compilation errors in all IndexReader implementations (expected — they still have `Retrieve` method)

- [ ] **Step 3: Commit**

```bash
git add segment/field_index.go
git commit -m "refactor(segment): add BlockContext, rename IndexReader.Retrieve to MatchQuery"
```

---

### Task 2: Update `DictIndex` for the default FlatDict path

**Files:**
- Modify: `segment/dict_index.go`

**Interfaces:**
- Consumes: `BlockContext.Dict` (`*FlatDict`), `BlockContext.Pl` (`[]byte`), `NewPostingListAt`, `core.NewTerm`
- Produces: `DictIndex` implementing `IndexReader`

- [ ] **Step 1: Update `segment/dict_index.go` to use MatchQuery**

The `DictIndex` should already implement the updated interface. Verify it has `MatchQuery(ctx BlockContext, ...)` method.

- [ ] **Step 2: Run segment tests**

Run: `go test ./segment/ -v -run TestDictIndex`
Expected: tests pass

- [ ] **Step 3: Commit**

```bash
git add segment/dict_index.go
git commit -m "refactor(segment): update DictIndex to use MatchQuery with BlockContext"
```

---

### Task 3: Update segment reader to use `BlockContext` and route default through containers

**Files:**
- Modify: `segment/segment_reader.go`

**Interfaces:**
- Consumes: `BlockContext`, `DictIndex`, `IndexReader.MatchQuery`
- Produces: updated `IndexQuery` method, simplified `GetPostingsByTerm`

- [ ] **Step 1: Update `IndexQuery` to build `BlockContext` and call `MatchQuery`**

In `segment/segment_reader.go`, replace the `IndexQuery` method:

```go
// IndexQuery dispatches a query to a registered container and returns
// posting iterators. kind is the container type (e.g. "default",
// "ac_matcher", "ext_range").
func (sr *SegmentReader) IndexQuery(field core.BEField, kind string, query interface{}) ([]core.PostingIterator, error) {
	blk, ok := sr.lookupBlock(field)
	if !ok {
		return nil, core.ErrUnknownQueryField
	}
	cr, ok := blk.readers[kind]
	if !ok {
		return nil, nil
	}
	ctx := BlockContext{Dict: blk.dict, Pl: blk.pl}
	return cr.MatchQuery(ctx, field, query)
}
```

- [ ] **Step 2: Rewrite `GetPostingsByTerm` as a thin wrapper**

Replace `GetPostingsByTerm`:

```go
// GetPostingsByTerm returns a posting iterator for a physical term in the
// merged (all-K) posting list. This is a convenience wrapper around
// IndexQuery with the default dict container.
func (sr *SegmentReader) GetPostingsByTerm(field core.BEField, term string) (core.PostingIterator, error) {
	iters, err := sr.IndexQuery(field, core.IndexNameDefault, term)
	if err != nil {
		return nil, err
	}
	if len(iters) == 0 {
		return nil, nil
	}
	return iters[0], nil
}
```

- [ ] **Step 3: Update `NewSegmentReaderWithOptions` to register DictIndex in the readers map**

In `segment/segment_reader.go`, in the `NewSegmentReaderWithOptions` function, after loading all blocks, ensure the default dict container is registered for fields that have a dict but no explicit reader:

After the block loading loop, add:

```go
	// Ensure fields with a dict get the default DictIndex registered.
	for _, blk := range blocks {
		if blk.dict != nil {
			if blk.readers == nil {
				blk.readers = make(map[string]IndexReader)
			}
			if _, ok := blk.readers[core.IndexNameDefault]; !ok {
				blk.readers[core.IndexNameDefault] = DictIndex{dict: blk.dict}
			}
		}
	}
```

- [ ] **Step 4: Run segment tests**

Run: `go test ./segment/ -v`
Expected: PASS — `GetPostingsByTerm` delegates to `IndexQuery` which uses `DictIndex`

- [ ] **Step 5: Commit**

```bash
git add segment/segment_reader.go
git commit -m "refactor(segment): route default dict through IndexQuery+DictIndex"
```

---

### Task 4: Update all IndexReader implementations (`Retrieve` → `MatchQuery`)

**Files:**
- Modify: `segment/range_index.go`
- Modify: `segment/ac_index.go`
- Modify: `container/mph/reader.go`
- Modify: `container/example/example.go`

**Interfaces:**
- Consumes: `BlockContext` (replaces `postingBlock []byte`)
- Produces: updated `MatchQuery` methods

- [ ] **Step 1: Update `RangeIndex` in `segment/range_index.go`**

Replace the `Retrieve` method with `MatchQuery`:

```go
// MatchQuery implements IndexReader by performing a stabbing query on the
// segment tree, returning posting cursors.
func (ri *RangeIndex) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	point, ok := query.(int64)
	if !ok {
		return nil, fmt.Errorf("range container expects int64 query, got %T", query)
	}
	return ri.Stab(field, point)
}
```

- [ ] **Step 2: Update `ACIndex` in `segment/ac_index.go`**

Replace the `Retrieve` method with `MatchQuery`:

```go
// MatchQuery implements IndexReader by performing AC matching against the
// query text and returning posting cursors into the posting block.
func (ac *ACIndex) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	text, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("ac container expects string query, got %T", query)
	}
	refs := ac.MatchPostingRefs(text)
	if len(refs) == 0 {
		return nil, nil
	}
	iters := make([]core.PostingIterator, 0, len(refs))
	for _, ref := range refs {
		pl, err := NewPostingListAt(ctx.Pl, ref)
		if err != nil {
			continue
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, text)))
	}
	return iters, nil
}
```

- [ ] **Step 3: Update `mph.Reader` in `container/mph/reader.go`**

Replace the `Retrieve` method with `MatchQuery`:

```go
func (r *Reader) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	term, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("mph: query must be string, got %T", query)
	}
	val := r.chd.Get([]byte(term))
	if val == nil {
		return nil, nil
	}
	ref, err := decodePostingRef(val)
	if err != nil {
		return nil, err
	}
	pl, err := segment.NewPostingListAt(ctx.Pl, ref)
	if err != nil {
		return nil, fmt.Errorf("mph: term %q posting: %w", term, err)
	}
	return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, term))}, nil
}
```

- [ ] **Step 4: Update `PrefixIndex` in `container/example/example.go`**

Replace the `Retrieve` method with `MatchQuery`:

```go
// MatchQuery returns posting cursors for every stored prefix that prefixes the
// query string. Linear scan keeps the template simple; production containers
// should exploit their layout (binary search, trie, interval tree...).
func (r *PrefixIndex) MatchQuery(ctx segment.BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	q, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("example: query must be string, got %T", query)
	}
	var iters []core.PostingIterator
	for _, e := range r.entries {
		if !strings.HasPrefix(q, e.term) {
			continue
		}
		pl, err := segment.NewPostingListAt(ctx.Pl, e.ref)
		if err != nil {
			return nil, fmt.Errorf("example: term %q posting: %w", e.term, err)
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, e.term)))
	}
	return iters, nil
}
```

- [ ] **Step 5: Run build to verify all implementations compile**

Run: `go build ./...`
Expected: PASS — all `MatchQuery` signatures match the interface

- [ ] **Step 6: Commit**

```bash
git add segment/range_index.go segment/ac_index.go container/mph/reader.go container/example/example.go
git commit -m "refactor: rename Retrieve→MatchQuery in all IndexReader implementations"
```

---

### Task 5: Simplify engine `initCursors` to single path

**Files:**
- Modify: `engine/searcher.go`

**Interfaces:**
- Consumes: `SegmentReader.IndexQuery` (now handles all containers including default)
- Produces: simplified `initCursors` with no if/else branching

- [ ] **Step 1: Rewrite the query dispatch loop in `initCursors`**

In `engine/searcher.go`, replace the current routing block:

```go
		var iterators []core.PostingIterator
		for _, seg := range e.segments {
			for _, q := range ef.queries {
				iters, err := seg.IndexQuery(field, containerName, q.Value)
				if err == nil && len(iters) > 0 {
					iterators = append(iterators, iters...)
				}
			}
		}
```

Where `containerName` defaults to `core.IndexNameDefault` (`"default"`) when the field has no custom container:

Update the containerName resolution:

```go
		// Determine the field's container type once per field.
		containerName := core.IndexNameDefault
		if fc, ok := e.schemaCodec.Field(field); ok {
			c := fc.Meta.IndexType
			if c != "" && segment.HasIndex(c) {
				containerName = c
			}
		}
```

- [ ] **Step 2: Run engine tests**

Run: `go test ./engine/ -v`
Expected: PASS

- [ ] **Step 3: Run full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add engine/searcher.go
git commit -m "refactor(engine): unify initCursors to single IndexQuery path"
```

---

### Task 6: Update test files calling `.Retrieve(`

**Files:**
- Modify: `container/mph/mph_test.go` — all `.Retrieve(` → `.MatchQuery(`
- Modify: `container/example/example_test.go` — all `.Retrieve(` → `.MatchQuery(`

**Interfaces:**
- Consumes: `BlockContext` (replaces `postingBlock []byte` in test calls)

- [ ] **Step 1: Update `container/mph/mph_test.go`**

Replace all `.Retrieve(postingBlock, ...)` calls with `.MatchQuery(segment.BlockContext{Pl: postingBlock}, ...)`. For tests that pass `nil` as postingBlock, use `.MatchQuery(segment.BlockContext{}, ...)`.

Example transformations:

```go
// Before:
iters, err := cr.Retrieve(pl, "field", "hello")
// After:
iters, err := cr.MatchQuery(segment.BlockContext{Pl: pl}, "field", "hello")

// Before:
iters, err := cr.Retrieve(nil, "f", "notfound")
// After:
iters, err := cr.MatchQuery(segment.BlockContext{}, "f", "notfound")

// Before:
iters, err := cr.Retrieve(nil, "f", 42)
// After:
iters, err := cr.MatchQuery(segment.BlockContext{}, "f", 42)
```

- [ ] **Step 2: Update `container/example/example_test.go`**

Same transformation pattern:

```go
// Before:
iters, err := cr.Retrieve(postingBlock, "path", "/api/v1/users")
// After:
iters, err := cr.MatchQuery(segment.BlockContext{Pl: postingBlock}, "path", "/api/v1/users")
```

- [ ] **Step 3: Run container tests**

Run: `go test ./container/... -v`
Expected: PASS

- [ ] **Step 4: Run full test suite**

Run: `make test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add container/mph/mph_test.go container/example/example_test.go
git commit -m "test: update container tests to use MatchQuery with BlockContext"
```

---

### Task 7: Final verification

- [ ] **Step 1: Run full test suite**

Run: `make test`
Expected: PASS — all tests pass

- [ ] **Step 2: Run vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Verify no remaining `.Retrieve(` on IndexReader**

Run: `rg "\.Retrieve\(" --type go` — should only match `engine.Retrieve` (BooleanEngine method), not container Retrieve

- [ ] **Step 4: Verify `BlockContext` is used consistently**

Run: `rg "BlockContext" --type go`
Expected: used in `field_index.go`, `dict_index.go`, `segment_reader.go`, all container implementations, and test files

- [ ] **Step 5: Verify diff is correct**

Run: `git diff --stat HEAD~7`
Expected: 12-13 files changed, `dict_index.go` updated

Run: `git log --oneline -8`
Expected: 7 new commits from this refactoring
