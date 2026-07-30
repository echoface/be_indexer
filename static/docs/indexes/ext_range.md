# ext_range Index (Segment-Tree Range Index)

`IndexType: "ext_range"`. A **numeric range** container. It answers a *stabbing
query* — "which indexed intervals contain the query point `q`?" — which is how
range predicates (`age > 18`, `18 <= age <= 35`) are matched. Internally it is a
**hybrid**: equality points go into a packed sorted key array, true ranges go into
a segment tree, and a query unions both.

- Source: [`segment/range_index.go`](../../../segment/range_index.go),
  [`segment/range_builder.go`](../../../segment/range_builder.go)
- Encoder: `ext_range` (compiles predicates into `[lo, hi]` intervals; equality
  becomes the degenerate `[v, v]`)
- Registered as: `core.IndexNameExtendRange`

## When to use

- **Numeric range / comparison predicates**: `>`, `>=`, `<`, `<=`, `between`, and
  mixed equality + range on the same field.
- Interval-containment matching where documents declare ranges and queries are
  points (or vice versa).

## When NOT to use

- Pure **exact equality** on a field with no range predicates — a plain
  dictionary ([`default`](default.md) / [`mph_dict`](mph_dict.md) /
  [`fst_dict`](fst_dict.md)) is lighter. (ext_range still handles equality
  efficiently via its point index, so a *mix* is fine.)
- **String / substring / geo** predicates — see [default](default.md),
  [ac_matcher](ac_matcher.md), [proximitygeo](proximitygeo.md).
- Values outside `int64` — the container keys on `int64`.

## Why the hybrid split

In practice most predicates on a range field are still equality (`= / in`), and
range predicates are the minority. Equality compiles to a degenerate interval
`[v, v]`. Feeding every equality point into the segment tree would inflate the
node count (tree size tracks the number of distinct endpoints) even though a point
needs no interval decomposition. So the builder **splits by shape**:

- **Point intervals** (`lo == hi`): a packed ascending `int64` key array with a
  parallel posting-offset array; queried by an allocation-free `int64` binary
  search.
- **Range intervals** (`lo < hi`, plus unbounded edges): a balanced **segment
  tree** over the distinct range endpoints only.

A query point `q` unions both sub-indexes. The two entry sets are disjoint by
construction (an `EntryID`'s interval is either a point or a range), so no double
counting.

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Point index probe | `O(log P)`, `P` = distinct equality keys |
| Segment-tree stab | `O(log m)` nodes on the root→leaf path, `m` = distinct range endpoints |
| Posting scan / merge | `O(posting length)` per matched node/point |
| Build | `O((P + m) log(P + m))` (sort endpoints, decompose ranges) |

Each range is decomposed into `O(log m)` canonical nodes at build time;
stabbing descends one root→leaf path, unioning the posting list of every node on
it. Canonical coverage is disjoint along a path, so an `EntryID` is collected at
most once.

## On-disk layout

Index block `<field>_ext_range` (all little-endian; posting region reuses the
8-byte-aligned `FlatPostingList` format):

```
┌──────────────────────────────────────────────┐
│ magic  "BEIRNG2\0"            (8 B)            │
│ nodeCount  (u32)   segment-tree node count     │
│ pointCount (u32)   distinct equality keys      │
├──────────────────────────────────────────────┤
│ nodes[]     nodeRecord × nodeCount  (24 B each) │
│ pointKeys[] int64 × pointCount      (ascending) │
│ pointOff[]  u32   × pointCount  (posting off+1) │
│ pad to 8 B                                      │
│ posting region: concatenated FlatPostingLists  │
└──────────────────────────────────────────────┘

nodeRecord (24 bytes)
┌──────────────┬────────────┬────────────┬────────────┐
│ split (i64)  │ left (i32) │ right (i32)│ postOff u32│
└──────────────┴────────────┴────────────┴────────────┘
 max value routed    child node    child node   posting off
 to the left child   indices       indices      +1 (0=empty)
```

- `split`: for an internal node, `q <= split` descends left, else right; unused
  for leaves (`left < 0`).
- `postOff` / `pointOff`: posting offset **+1** into the posting region; `0` means
  "no posting list here".
- Empty field → block omitted (both counts zero).

## Example

```go
fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "age": {
        ID:    1,
        Field: "age",
        FieldOption: be_indexer.FieldOption{
            IndexType: be_indexer.IndexNameExtendRange, // "ext_range"
            Encoder:   "ext_range",                     // interval encoder
        },
    },
}
// Document predicate: age between [18, 35]; query assignment: age = 20 → matches.
```

See the [index overview](README.md) for how this compares to the other
containers.
