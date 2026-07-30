# mph_dict Index (Minimal Perfect Hash)

`IndexType: "mph_dict"`. An exact-match dictionary backed by a **CHD minimal
perfect hash** (Compress-Hash-Displace). Every term maps to a distinct slot with
no collisions, so a lookup is a constant number of hash/array operations
regardless of vocabulary size. The term strings are **not** stored — only the
12-byte posting reference per key.

- Source: [`container/mph/`](../../../container/mph/)
  (`mph.go`, `builder.go`, `index.go`)
- Dependency: `github.com/alecthomas/mph`
- Registered as: `mph_dict`

## When to use

- **Very large vocabularies** of exact-match keys (millions+): device ids,
  user ids, cookie/hash values, opaque tokens.
- Keys are **high-entropy / opaque** — they share little structure, so prefix
  compression ([`fst_dict`](fst_dict.md)) would not help.
- You never need to **enumerate** the terms from the index or do reverse lookups.
  Only "is this exact key present, and where is its posting list?" matters.
- You want **constant-time** lookups and can accept a build-time hash
  construction pass.

## When NOT to use

- You need to **list/iterate** stored terms, or do prefix/range scans — mph does
  not keep the key strings. Use [`default`](default.md) or [`fst_dict`](fst_dict.md).
- Keys share heavy structure (URLs, paths, class names) where
  [`fst_dict`](fst_dict.md) would give both `O(len(term))` lookup **and** strong
  compression while still storing terms.
- Small vocabularies — the default FlatDict's `O(log n)` is already fast and
  avoids the extra dependency and hash-build cost.
- Range / substring / geo predicates — wrong tool entirely (see
  [ext_range](ext_range.md), [ac_matcher](ac_matcher.md),
  [proximitygeo](proximitygeo.md)).

> **Safety note:** because term strings are not stored, mph cannot verify that a
> queried key was actually in the build set. A minimal perfect hash returns *some*
> slot for any input; an absent key may collide onto an unrelated slot. Use
> mph_dict only when the query domain is trusted to be within the indexed key set,
> or when a rare false-positive posting match is acceptable and filtered
> downstream.

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Term lookup | `O(1)` — a few hash evaluations + array reads (~250 ns constant) |
| Posting scan / merge | `O(posting length)` with galloping `SkipTo` |
| Build | `O(n)` amortized CHD construction |

Lookup latency is **independent of vocabulary size** and independent of term
length (beyond hashing the bytes once).

## On-disk layout

The build serializes the CHD table via `mph`'s own writer into the index block
`<field>_mph_dict`. Each key's stored value is a fixed 12-byte posting reference:

```
mph value (per key), 12 bytes
┌────────────────────┬──────────────┐
│ Offset (u64)       │ Count (u32)  │
└────────────────────┴──────────────┘
 block-relative offset  #EntryIDs
 of FlatPostingList     in the list
```

The surrounding block bytes are the CHD structure (hash function parameters +
displacement/index tables produced by `chd.Write`). Because the value carries
both `Offset` and `Count`, the reader builds a posting cursor without touching the
`FlatPostingList` header. Term key bytes are consumed at build time to compute the
hash and are **not** retained.

## Example

```go
fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "device_id": {
        ID:    1,
        Field: "device_id",
        FieldOption: be_indexer.FieldOption{
            IndexType: "mph_dict",
            Encoder:   "default",
        },
    },
}
```

## mph_dict vs fst_dict

Both target large vocabularies and replace the FlatDict binary search. Pick based
on key structure:

| | mph_dict | fst_dict |
|:--|:---------|:---------|
| Lookup | `O(1)` | `O(len(term))`, vocab-independent |
| Stores terms | No | Yes (ordered, prefix-compressed) |
| Enumerable / prefix scan | No | Yes |
| Best key shape | opaque, high-entropy | structured, shared affixes |
| Compression | value table only | folds shared prefixes/suffixes |

See the [index overview](README.md) for the full comparison.
