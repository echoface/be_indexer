# proximitygeo (Geo Proximity over the Default Index)

`Encoder: "proximitygeo"`. Radius / proximity search over `(lat, lng)` points.
Unlike the other entries in this section, **proximitygeo is an encoder, not a
dedicated container** — it registers only a `PredicateEncoder` and stores its
output as plain terms in the **default** [FlatDict](default.md) index. There is no
`segment.RegisterIndex` call and no custom on-disk block type.

- Source: [`container/geo/encoder.go`](../../../container/geo/encoder.go)
- Dependency: `github.com/echoface/proximityhash`, geohash encoding
- Registered as: `parser.RegisterPredicateEncoder("proximitygeo")`

## How it works

The problem "is the query point within radius R of an indexed area?" is reduced to
**geohash cell membership**, which is plain exact-term matching:

- **Build (document side).** A `(lat, lng, radius)` predicate is expanded via
  `proximityhash` into the set of geohash cells covering that circle, compressed
  to the coarsest precision whose cell width fits the radius, then stored as plain
  geohash-string terms in the default dictionary.
- **Query (assignment side).** The query point is geohash-encoded at the finest
  precision and expanded into **all prefixes** of length
  `[minPrecision, maxQueryPrecision]`. Each prefix is one exact-term lookup; a hit
  means the point falls inside a cell the document covered.

Because both sides reduce to geohash strings, retrieval is just default-index term
lookups — one per query prefix.

## Parameters (current constants)

| Constant | Value | Meaning |
|:---------|:------|:--------|
| `minPrecision` | 3 | Coarsest stored/queried cell (bounds compression & cell count) |
| `maxQueryPrecision` | 8 | Finest geohash length (~38 m cell) |
| `maxRadiusMeters` | 1,000,000 | Max supported radius (~128 covering cells at precision 3) |

A radius outside `(0, maxRadiusMeters]` is rejected at build time.

## When to use

- **Radius / "near me"** filtering: match documents whose covered area is within
  a distance of the query point.
- You want geo matching **without** a specialized spatial index — it rides on the
  battle-tested default container.

## When NOT to use

- **Exact-distance** or precise polygon/geometry queries — geohash cells are an
  approximation bounded by `maxQueryPrecision` (~38 m) and the radius→precision
  table. This is proximity, not exact geometry.
- Radii larger than `maxRadiusMeters` (1000 km).
- Non-geographic values — it only makes sense for `(lat, lng)`.

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Query | `(maxQueryPrecision - minPrecision + 1)` default-index lookups (≤ 6), each `O(log n)` |
| Posting scan / merge | `O(posting length)` per matched cell |
| Build | `O(covering cell count)` per predicate (bounded by radius/precision) |

## On-disk layout

None of its own — the covering geohash cells are ordinary terms in the field's
[`FlatDict`](default.md#on-disk-layout) block, with posting lists in the standard
[`FlatPostingList`](../../../segment/posting_list.go) format. To inspect or size a
proximitygeo field, read it as a default-index field.

## Example

```go
fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "location": {
        ID:    1,
        Field: "location",
        FieldOption: be_indexer.FieldOption{
            IndexType: be_indexer.IndexNameDefault, // stored in the default container
            Encoder:   "proximitygeo",              // geo covering-cell encoder
        },
    },
}
// Document: a point/area with a radius → covering geohash cells.
// Query: a (lat, lng) point → prefixes matched against those cells.
```

See the [index overview](README.md) for how the containers and encoders fit
together.
