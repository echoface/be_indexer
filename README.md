# be_indexer

High-performance Boolean Expression Indexing SDK based on the VLDB 09 paper *"Indexing Boolean Expressions."*

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  be_indexer (root) — Public API Facade                   │
│  BuildSegment / BuildSegments / NewEngine / NewSegmentReader │
└──────┬───────────────┬───────────────┬──────────────────┘
       │               │               │
  ┌────▼────┐    ┌─────▼──────┐   ┌───▼──────┐
  │ builder │    │   engine    │   │ segment  │
  │ compile │    │  retrieve   │   │ mmap     │
  │ docs→seg│    │  K-Groups   │   │ storage  │
  └────┬────┘    └─────┬──────┘   └───┬──────┘
       │               │               │
       └───────────────┼───────────────┘
                       │
                  ┌────▼────┐
                  │  core   │
                  │  types  │
                  └─────────┘
```

### Packages

| Package | Responsibility |
|---------|---------------|
| `core/` | Foundational types: `Document`, `Conjunction`, `Predicate`, `ConjID`/`EntryID` encoding, `TermIterator`, `FieldCursor`, `ResultCollector`, `RetrieveObserver` |
| `segment/` | Physical storage: binary segment writer with mmap reader, `FlatDict`, `FlatPostingList`, DAT-based `ACMatcher` |
| `builder/` | Document compiler: converts DNF `Document` → inverted postings → binary segment |
| `engine/` | Query engine: `BooleanEngine` with K-Groups multiway merge and exclude short-circuiting |
| `parser/` | Value tokenizers: number, geohash — convert values to string terms |
| `be_indexer` (root) | Public API — type re-exports, factory functions, `RetrieveObserver` |

### Dependency Direction

Strictly unidirectional (no cycles):

```
be_indexer → builder → segment → core
be_indexer → engine  → segment → core
be_indexer → core
```

## Quick Start

```go
import "github.com/echoface/be_indexer"

// 1. Build a segment
buf := new(bytes.Buffer)
fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "age":  {ID: 1, Field: "age", FieldOption: be_indexer.FieldOption{Tokenizer: "number"}},
    "city": {ID: 2, Field: "city", FieldOption: be_indexer.FieldOption{Tokenizer: "default"}},
}

docs := []*be_indexer.Document{
    be_indexer.NewDocument(1).AddConjunction(
        be_indexer.NewConjunction().In("age", 25).In("city", "shanghai"),
    ),
    be_indexer.NewDocument(2).AddConjunction(
        be_indexer.NewConjunction().In("city", "beijing"),
    ),
}

wildcards, err := be_indexer.BuildSegment(buf, fields, docs)

// 2. Load and query
reader, _ := be_indexer.NewSegmentReader(buf.Bytes())
engine := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})

results, _ := engine.Retrieve(be_indexer.Assignments{
    "age": 25, "city": "shanghai",
})
// results = [1]
```

## Multi-Segment Build

For large document sets (>10M docs), split across multiple segments:

```go
wildcards, segCount, err := be_indexer.BuildSegments(
    func(segIdx int) (io.Writer, error) {
        f, err := os.Create(fmt.Sprintf("segment_%d.bin", segIdx))
        return f, err
    },
    fields,
    docs,
    be_indexer.BuildOptions{MaxDocsPerSegment: 100000},
)
```

## Observability

Inject a `RetrieveObserver` for instrumentation:

```go
type myObserver struct{}

func (o *myObserver) OnRetrieveStart(ctx *be_indexer.RetrieveContext) { /* ... */ }
func (o *myObserver) OnRetrieveEnd(ctx *be_indexer.RetrieveContext)   { /* ... */ }
func (o *myObserver) OnMatch(docID be_indexer.DocID, conjID be_indexer.ConjID) { /* ... */ }
func (o *myObserver) OnExcludeSkip(docID be_indexer.DocID) { /* ... */ }
func (o *myObserver) OnCursorInit(k int, fieldCount int) { /* ... */ }

results, _ := engine.Retrieve(queries, be_indexer.WithObserver(&myObserver{}))
```

## Core Algorithm

### ID Encoding

```
ConjID: [ reserved(4bit) | K(8bit) | index(8bit) | negSign(1bit) | docID(43bit) ]
EntryID: [ ConjID(60bit) | empty(3bit) | incl/excl(1bit) ]
```

### K-Groups

Postings are partitioned by K (number of Include predicates in a conjunction).
Query evaluates K from maxK down to 0, fetching only relevant posting lists.

For K=0 (pure exclude conjunctions), a wildcard posting list covers all such documents.

### Exclude Short-Circuiting (Z-Entry)

When the K-Groups merge finds an `exclude` EntryID whose ConjID matches the current end position, all remaining cursors skip past that document — turning exclude conditions into a pruning optimization.

## Segment Binary Format

```
+--------------------------------------------------+
| Magic Number (8 bytes, "BEIDX\0\0\1")            |
+--------------------------------------------------+
| Block: k{N}_{field}_postings (FlatPostingList)    |
+--------------------------------------------------+
| Block: k{N}_{field}_dict    (FlatDict)            |
+--------------------------------------------------+
| Block: k{N}_{field}_ac      (ACMatcher, optional) |
+--------------------------------------------------+
| ... more (K, field) blocks ...                    |
+--------------------------------------------------+
| Metadata Block (JSON)                             |
+--------------------------------------------------+
| Footer: MetaOffset (8 bytes, uint64 LE)           |
+--------------------------------------------------+
```

## Build & Test

```bash
make build    # CGO_ENABLED=0 go build ./...
make test     # CGO_ENABLED=0 go test -v -cover ./...
```

## Constraints

- `DocID` range: `[-2^43, 2^43]`
- Max conjunctions per document: < 256
- Max K (conjunction size): < 256
- Single-field index values: `EntryID` (uint64)

# 调研的一些开源库

FST： github.com/blevesearch/vellum

## License

MIT
