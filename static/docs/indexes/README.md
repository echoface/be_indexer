# Field Index Types

`be_indexer` stores every field's terms in a **pluggable index container**. Each
container is registered under a name and selected per field via
`FieldOption.IndexType`:

```go
fields := be_indexer.Schema{
    "city": {IndexType: "fst_dict", Encoder: "default"},
}
```

`IndexType` (which container stores the posting lists) and `Encoder` (how a typed
value becomes lookup terms) are **independent axes**. The default encoder covers
exact-term and numeric values; special encoders (`ext_range`, `proximitygeo`)
transform values before they hit the container.

All containers ultimately point at the same posting-list primitive
([`FlatPostingList`](../../../segment/posting_list.go)): an 8-byte-aligned, zero-copy
array of `EntryID`s that the engine merges with galloping `SkipTo`. The
containers differ only in **how a term resolves to a posting-list offset**.

---

## Choosing a container

| Container | `IndexType` | Best for | Lookup cost | Stores term strings? | External dep |
|:----------|:------------|:---------|:------------|:---------------------|:-------------|
| [Default (FlatDict)](default.md) | `"default"` / `""` | Exact match on small/medium vocabularies | `O(log n)` binary search | Yes (enumerable) | No |
| [mph_dict](mph_dict.md) | `"mph_dict"` | Huge vocabularies of opaque keys (ids, hashes) | `O(1)` | No | `alecthomas/mph` |
| [fst_dict](fst_dict.md) | `"fst_dict"` | Huge vocabularies with shared prefixes/suffixes (URLs, paths, tokens) | `O(len(term))`, vocab-independent | Yes (ordered/prefix) | `blevesearch/vellum` |
| [ac_matcher](ac_matcher.md) | `"ac_matcher"` | Multi-pattern substring / keyword screening | `O(len(text))` per query, all patterns at once | Patterns folded into automaton | No |
| [ext_range](ext_range.md) | `"ext_range"` | Numeric range predicates (`>`, `<`, `between`) | Point `O(log P)` + tree path `O(log m)` | No (keys packed) | No |

Encoder-only capability (no dedicated container):

| Capability | Encoder | Runs on | Notes |
|:-----------|:--------|:--------|:------|
| [proximitygeo](proximitygeo.md) | `"proximitygeo"` | Default (FlatDict) | Geohash covering cells stored as plain terms |

### Decision guide

- **Exact string/number equality, vocabulary up to ~1M** → **default**. Simplest,
  no dependency, enumerable, good cache behavior.
- **Exact equality on very large, high-entropy key sets** (device ids, hashes,
  UUIDs) where you never enumerate → **mph_dict**. Constant-time, and it does not
  pay to store the key strings.
- **Exact equality on very large key sets with structural redundancy** (URLs,
  file paths, package/class names, natural-language tokens) → **fst_dict**. The
  transducer folds shared affixes for large on-disk savings; lookup time does not
  grow with the vocabulary.
- **"Does this text contain any of my N keywords?"** → **ac_matcher**. One pass
  over the input scores every pattern simultaneously.
- **Numeric ranges / interval containment** (`age > 18`, `18 <= age <= 35`) →
  **ext_range**. Turns a stabbing query into a logarithmic tree walk plus a point
  lookup.
- **Radius / proximity search over lat-lng** → **proximitygeo** encoder over the
  default container.

---

## On-disk model (shared context)

A segment is a sequence of 8-byte-aligned blocks. Each indexed field contributes:

1. A **postings block** — one or more `FlatPostingList`s (the `EntryID` arrays).
2. An **index block** named `<field>_<kind>` (e.g. `age_dict`, `city_fst_dict`,
   `tag_ac_matcher`, `age_ext_range`) that maps terms → posting offsets.

The per-index docs describe the exact byte layout of each index block. The shared
posting-list layout is:

```
FlatPostingList
┌───────────────┬───────────────┬─────────────────────────────┐
│ Count (u32)   │ Pad (u32)     │ EntryID × Count (u64 each)  │
└───────────────┴───────────────┴─────────────────────────────┘
 4 bytes          4 bytes         8·Count bytes, 8-byte aligned
```

`Pad` keeps the `EntryID` array 8-byte aligned so the reader can
`unsafe.Slice` it directly over the mapped bytes with zero copy.

---

## Writing a custom container

Implement `segment.IndexBuilder` / `segment.IndexReader` and register at `init`:

```go
segment.RegisterIndex("my_index", segment.IndexDef{
    Reader:  newReaderFactory,
    Builder: newBuilderFactory,
})
```

If your values also need custom term encoding, register an encoder:

```go
parser.RegisterPredicateEncoder("my_index", factory)
```

See [`container/example/`](../../../container/example/) for a complete, tested
template, and [`container/fst/`](../../../container/fst/) for a production container
that replaces the default dictionary.
