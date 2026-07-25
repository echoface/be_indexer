# mph_dict Container Design

## Overview

Add an optional `"mph_dict"` container that replaces `FlatDict` binary search with
O(1) minimal perfect hash (CHD algorithm via `alecthomas/mph`) for fields with
large vocabularies. Users opt in per-field via schema, keeping the existing
FlatDict path unchanged for all other fields.

## Motivation

`FlatDict.Find()` uses binary search O(log n) over sorted term arrays. For fields
with hundreds of thousands of distinct terms, the O(log n) cost dominates the
query hot path. mph's CHD algorithm provides O(1) exact-match lookups with
~2-4 bits/key overhead and native mmap support, making it a natural fit.

## Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Integration pattern | Full container (ac_matcher-style) | No changes to `segment_reader.go` internals; pure extension via existing `ContainerReader`/`ContainerBuilder` + `RegisterContainer` infrastructure |
| Opt-in granularity | Per-field via schema `FieldOption{Container: "mph_dict"}` | Users mix mph and FlatDict fields in one segment |
| Coexistence with FlatDict | Replace entirely for opted-in fields | Saves disk, simpler; mph is deterministic so no fallback needed |
| mph value encoding | `PostingRef` serialized as 12 bytes `[Offset u64 LE][Count u32 LE]` | Zero indirection: `chd.Get()` returns exact data to create posting cursor |
| mmap strategy | `mph.Mmap(b)` for Reader | Native zero-copy support, consistent with project's mmap architecture |

## Architecture

```
container/mph/
├── mph.go          # ContainerName const + init() registration
├── encoder.go      # PredicateEncoder impl (exact-match, QueryKind="mph_dict")
├── builder.go      # SortableBuilder impl (accumulates term→PostingRef, serializes mph table)
├── reader.go       # ContainerReader impl (mph.Mmap, O(1) Get)
└── mph_test.go     # Round-trip + end-to-end tests + benchmarks
```

**Dependencies**: `github.com/alecthomas/mph` (new, BSD-3-Clause), plus existing
`core`, `parser`, `segment` packages.

## Data Flow

### Build Path

```
Encoder.Build(expr) → []EncodedPosting{Record: term}
  → framework sorts by term, groups, writes FlatPostingList
  → SortableBuilder.AddKeyedPosting(key, ref, entries)  // accumulate (term, ref) pairs
  → Build():
      mph.Builder() → for each pair: Add(term, PostingRef.Bytes())
      → chdBldr.Build() → *mph.CHD
      → chd.Write(&buf) → []byte  // block stored in segment
```

### Query Path

```
Encoder.Query(value) → EncodedQuery{Kind: "mph_dict", Value: term}
  → engine initCursors default branch
  → SegmentReader.ContainerQuery(field, "mph_dict", term)
  → Reader.Retrieve(postingBlock, field, term):
      chd.Get([]byte(term)) → []byte  // O(1)
      if val == nil: return nil, nil  // no match
      decode PostingRef from 12 bytes
      NewPostingListAt(postingBlock, ref) → flatPostingCursor
      return []core.PostingIterator{cursor}
```

The engine's K-Groups merge algorithm consumes the returned cursor identically
to FlatDict-produced cursors — mph is transparent above the segment layer.

## Component Specifications

### Encoder (`encoder.go`)

Identical semantics to the built-in `ExactTermEncoder`, but emits
`QueryKind("mph_dict")` instead of `QueryKindTerm`. This single difference
routes queries through `ContainerQuery` rather than `GetPostingsByTerm`.

```go
type Encoder struct{}

func (Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error)
    // EQ only, emits EncodedPosting{Record: term} for each distinct value

func (Encoder) Query(value interface{}) ([]parser.EncodedQuery, error)
    // Emits EncodedQuery{Kind: "mph_dict", Value: term} for each value string
```

### Builder (`builder.go`)

Implements `segment.SortableBuilder`. The framework handles external sort and
group-by-key; the builder only accumulates pairs and serializes the mph table.

```go
type Builder struct {
    terms []termRef
}

type termRef struct {
    term string
    ref  segment.PostingRef
}

func (b *Builder) RecordToKey(record any) []byte
    // Extracts key bytes from a record (string or []byte)

func (b *Builder) AddKeyedPosting(key []byte, ref segment.PostingRef, entries []core.EntryID) error
    // Accumulates (term, ref) pair

func (b *Builder) Build() ([]byte, error)
    // 1. Create mph.Builder()
    // 2. For each term: Add(term, littleEndianEncode(ref))
    // 3. chdBuilder.Build()
    // 4. chd.Write(&bytes.Buffer)
    // 5. Return buffer bytes
```

PostingRef encoding: `[Offset uint64 LE][Count uint32 LE]` = 12 bytes.

### Reader (`reader.go`)

Implements `segment.ContainerReader`. Zero-copy via `mph.Mmap`. Safe for
concurrent `Retrieve` calls (read-only state).

```go
type Reader struct {
    chd *mph.CHD
}

func NewReader(b []byte) (segment.ContainerReader, error)
    // mph.Mmap(b) — zero-copy, returns *CHD backed by the segment bytes

func (r *Reader) Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error)
    // 1. Assert query is string
    // 2. val := chd.Get([]byte(query))
    // 3. if val == nil: return nil, nil (term not indexed)
    // 4. Decode PostingRef from val[0:12]
    // 5. pl, err := segment.NewPostingListAt(postingBlock, ref)
    // 6. return []core.PostingIterator{pl.NewPostingCursor(core.NewTerm(field, query))}
```

### Registration (`mph.go`)

```go
const ContainerName = "mph_dict"

func init() {
    parser.RegisterPredicateEncoder(ContainerName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
        return Encoder{}, nil
    })
    segment.RegisterContainer(ContainerName, segment.ContainerDef{
        Reader:  func(b []byte) (segment.ContainerReader, error) { return NewReader(b) },
        Builder: func() segment.ContainerBuilder { return NewBuilder() },
    })
}
```

## Error Handling

- **Build side**: `Build()` returns wrapped mph errors with descriptive context
- **Load side**: `NewReader` trusts `mph.Mmap` to validate its block at `Get()` time
- **Query side**: Non-string query → descriptive error (consistent with all containers)
- **Term not found**: `chd.Get()` returns nil → `Retrieve` returns `nil, nil` (no match)
- **Corrupt PostingRef value**: `NewPostingListAt` validates offset/count bounds

## Testing

Following `container/example/example_test.go` patterns, using GoConvey BDD style:

1. **Round-trip**: Builder → bytes → Reader → Retrieve → verify EntryID returned
2. **End-to-end**: `BuildSegment` → `NewEngine` → `Retrieve` with assignments
3. **Mixed containers**: Field with `"mph_dict"` + field with `"default"` composing K=2 conjunctions
4. **Edge cases**: Empty dict, non-string query, truncated block, term-not-found, single-entry
5. **Benchmark**: Compare mph vs FlatDict lookups at 10K/100K/1M term scales

## Performance Tradeoffs

| Aspect | FlatDict | mph_dict |
|---|---|---|
| Lookup | O(log n), ~50-100ns for 100K terms | O(1), ~200ns constant |
| Build | O(n log n), sort only | O(n) expected, CHD construction |
| Memory | Sorted array, compact | ~2-4 bits/key overhead + base/check arrays |
| mmap | Manual binary parsing | Native Mmap() support |
| Best for | Small-medium vocabularies | Large vocabularies (100K+) |

## Acceptance Criteria

- [ ] All existing tests pass without modification
- [ ] mph round-trip test passes (build → read → retrieve)
- [ ] mph end-to-end test passes (full engine integration)
- [ ] mph + FlatDict mixed field test passes (K=2 conjunction composition)
- [ ] `go vet ./...` clean
- [ ] No allocation on the Retrieve hot path (verify via benchmark)
