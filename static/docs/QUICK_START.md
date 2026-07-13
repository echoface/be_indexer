# Quick Start

## 1. Define Schema

Define which fields to index and how values are tokenized:

```go
import "github.com/echoface/be_indexer"

fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "age": {
        ID:    1,
        Field: "age",
        FieldOption: be_indexer.FieldOption{
            Container: be_indexer.IndexNameDefault,
            Tokenizer: "number",
        },
    },
    "city": {
        ID:    2,
        Field: "city",
        FieldOption: be_indexer.FieldOption{
            Container: be_indexer.IndexNameDefault,
            Tokenizer: "default",
        },
    },
}

// For range queries ("age > 18"):
fields["age"].FieldOption.Container = be_indexer.IndexNameExtendRange

// For substring matching ("tag contains 'premium'"):
fields["tag"].FieldOption.Container = be_indexer.IndexNameACMatcher
```

## 2. Build Documents

Documents encode ad targeting rules in DNF (OR of ANDs):

```go
// Doc 1: age in [18,25] AND city = "beijing"
doc1 := be_indexer.NewDocument(1)
c1 := be_indexer.NewConjunction()
c1.In("age", be_indexer.NewIntValues(18, 25))
c1.In("city", be_indexer.NewStrValues("beijing"))
doc1.AddConjunction(c1)

// Doc 2: city NOT IN "rural"  (K=0 wildcard)
doc2 := be_indexer.NewDocument(2)
c2 := be_indexer.NewConjunction()
c2.NotIn("city", be_indexer.NewStrValues("rural"))
doc2.AddConjunction(c2)

// Doc 3: age > 30 AND (tag contains "vip" OR "premium")
doc3 := be_indexer.NewDocument(3)
c3 := be_indexer.NewConjunction()
c3.GreaterThan("age", 30)
c3.In("tag", be_indexer.NewStrValues("vip"))
c3.In("tag", be_indexer.NewStrValues("premium"))
doc3.AddConjunction(c3)

docs := []*be_indexer.Document{doc1, doc2, doc3}
```

## 3. Build & Query (Single Segment)

```go
// Build
buf := new(bytes.Buffer)
wildcards, err := be_indexer.BuildSegment(buf, fields, docs)

// Load
reader, err := be_indexer.NewSegmentReader(buf.Bytes())
engine := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})

// Query
result, err := engine.Retrieve(be_indexer.Assignments{
    "age":  []int{20},
    "city": []string{"beijing"},
})
// result → BitmapDocSet containing DocID 1 and 2
```

## 4. Production: Full + Delta with mmap

In production, data is built offline and loaded via mmap for zero-copy serving.

### Build

```go
// Full index (daily batch)
fullOpt := be_indexer.FullIndexBuildOption{
    Root:       "/data/index",
    Generation: 20240701,
    Fields:     fields,
}
full := be_indexer.NewFullIndexBuilder(fullOpt)
for _, doc := range allDocs {
    if err := full.AddDocument(doc); err != nil {
        // handle
    }
}
fullDesc, err := full.Build()
// → /data/index/full/full-20240701/segment-000000.bei ...
```

```go
// Delta index (incremental, every 5 min)
deltaOpt := be_indexer.DeltaIndexBuildOption{
    Root:                   "/data/index",
    Generation:             202407010001,
    FromWatermarkExclusive: 0,
    ToWatermarkInclusive:   snapshotWatermark,
    Fields:                 fields,
}
delta := be_indexer.NewDeltaIndexBuilder(deltaOpt)
for _, m := range mutations { // Mutation{Op: Upsert/Delete, DocID, Doc}
    delta.Add(m)
}
deltaDesc, err := delta.Build()
// → /data/index/delta/delta-202407010001/segment-000000.bei
//                                      changed_docs.bin
//                                      deleted_docs.bin
```

```go
// Publish manifest
manifest, err := be_indexer.NewSnapshotManifest(be_indexer.SnapshotManifestRequest{
    Full:   fullDesc,
    Deltas: []be_indexer.DeltaIndexDescriptor{deltaDesc},
})
be_indexer.PublishManifest("/data/index", "manifest-1.json", manifest)
// → /data/index/manifests/manifest-1.json
// → /data/index/CURRENT → "manifests/manifest-1.json"
```

### Serve

```go
// Cold start
engine, err := be_indexer.OpenIndex("/data/index", fields,
    be_indexer.LoaderOptions{UseMmap: true},
)
results, err := engine.Retrieve(assignments)
```

```go
// With live reload
holder := be_indexer.NewIndexHolder("/data/index", fields,
    be_indexer.LoaderOptions{UseMmap: true},
)
go func() {
    ctx := context.Background()
    holder.Watch(ctx, 30*time.Second) // auto-reload on manifest change
}()

// All readers get the latest snapshot
results, err := holder.Engine().Retrieve(assignments)
```

## 5. Multi-Segment Build

For large datasets, split across segments to bound peak memory:

```go
wildcards, segCount, err := be_indexer.BuildSegments(
    func(segIdx int) (io.Writer, error) {
        return os.Create(fmt.Sprintf("segment_%d.bei", segIdx))
    },
    fields,
    docs,
    be_indexer.BuildOptions{MaxDocsPerSegment: 1_000_000},
)
```

## 6. Observability

```go
type obs struct{ matchCount int }

func (o *obs) OnRetrieveStart(ctx *be_indexer.RetrieveContext) {}
func (o *obs) OnRetrieveEnd(ctx *be_indexer.RetrieveContext)   {}
func (o *obs) OnMatch(docID be_indexer.DocID, conjID be_indexer.ConjID)   { o.matchCount++ }
func (o *obs) OnExcludeSkip(docID be_indexer.DocID)                       {}
func (o *obs) OnCursorInit(fieldCount int)                                {}

results, _ := engine.Retrieve(assignments,
    be_indexer.WithObserver(&obs{}),
)
```

## 7. Compaction

Monitor index health and trigger merges:

```go
manifest, _ := be_indexer.LoadSnapshot(root, fields, opts)

stats := be_indexer.CollectCompactStats(manifest)
decision := be_indexer.DecideCompact(stats, be_indexer.DefaultCompactPolicy)

switch decision.Decision {
case be_indexer.CompactDecisionMajor:
    // Full rebuild needed (many deltas / old full)
case be_indexer.CompactDecisionMinor:
    // Merge deltas (reduce delta count)
case be_indexer.CompactDecisionNone:
    // Healthy
}
```

---

## Migration from v1/v2

| Old (v1) | New (v3+) |
|:---------|:----------|
| `segment.MmapReader` | `segment.SegmentReader` |
| `segment.NewMmapReader(data)` | `segment.NewSegmentReader(data)` |
| `segment.OpenMmapFile(path)` | `segment.OpenSegmentFile(path)` |
| `engine.NewBooleanEngine(fields, wc, [reader])` | same signature (unchanged) |
| Wildcards returned from build, passed to engine | Wildcards embedded in segment; engine reads from segments (pass `nil`) |

The older `wildcards` parameter to `NewBooleanEngine` is still accepted but no longer used — the engine reads wildcards directly from segments for lazy K-way merge at query time.
