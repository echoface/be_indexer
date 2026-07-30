# fst_dict Index (Finite State Transducer)

`IndexType: "fst_dict"`. An exact-match term dictionary backed by an ordered
**Finite State Transducer** (via `blevesearch/vellum`). It replaces the default
FlatDict binary search with a transducer walk: shared prefixes and suffixes are
folded into a shared graph, so lookup time depends only on the **term length**,
not the vocabulary size, and structurally-similar keys compress heavily on disk.

- Source: [`container/fst/`](../../../container/fst/)
  (`fst.go`, `builder.go`, `index.go`)
- Dependency: `github.com/blevesearch/vellum`
- Registered as: `fst_dict`

## When to use

- **Very large vocabularies** where keys share structure: URLs, file/paths,
  package or class names, domain names, tokenized natural-language text.
- You want lookup latency that is **independent of vocabulary size** — `O(len(term))`
  holds whether the field has 100K or 10M distinct terms.
- You care about **on-disk size**: the FST folds common prefixes/suffixes, often
  far smaller than storing every term verbatim (as FlatDict does) — see the size
  note below.
- You still want an **ordered / enumerable** dictionary (unlike
  [`mph_dict`](mph_dict.md), the FST preserves terms and supports ordered
  iteration / prefix traversal at the vellum level).

## When NOT to use

- Small/medium vocabularies — [`default`](default.md) FlatDict is simpler, has no
  external dependency, and `O(log n)` is already fast.
- Keys are **opaque and high-entropy** (random ids, hashes) with no shared
  structure — the FST cannot compress them, and [`mph_dict`](mph_dict.md) gives
  `O(1)` lookups without storing keys at all.
- Range, substring, or geo predicates — see [ext_range](ext_range.md),
  [ac_matcher](ac_matcher.md), [proximitygeo](proximitygeo.md).

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Term lookup | `O(len(term))` transducer walk, **independent of vocabulary size** |
| Posting scan / merge | `O(posting length)` with galloping `SkipTo` |
| Build | `O(n log n)` (keys must be inserted in ascending order) |

### Measured lookup latency (verified via `BenchmarkFST_Find`)

| Vocabulary size | Hit | Miss |
|:----------------|:----|:-----|
| 100,000 terms | ~306 ns, 128 B, 5 allocs | ~19 ns, 0 B, 0 allocs |
| 1,000,000 terms | ~257 ns, 128 B, 5 allocs | ~47 ns, 0 B, 0 allocs |

Latency is essentially flat between 100K and 1M terms, confirming
vocabulary-independence. The **miss path is allocation-free** (0 allocs), and the
hit path is minimized by pooling readers (see below). Numbers are from
`go test -bench BenchmarkFST_Find` on the reference machine — treat them as
relative, not absolute.

### On-disk size

The FST folds shared prefixes/suffixes into a single graph, so for
structurally-redundant vocabularies (URLs, paths, class names) the term
dictionary can be **dramatically smaller** than FlatDict, which stores every term
verbatim. In a deliberately prefix-heavy micro-demo the FST block came out roughly
three orders of magnitude smaller than the equivalent FlatDict block — an
illustrative best case, not a typical ratio. Real savings depend entirely on how
much structure the keys share; opaque/high-entropy keys compress poorly (prefer
[`mph_dict`](mph_dict.md) there).

## Allocation & concurrency design

vellum's plain `FST.Get` allocates a fresh internal state per call, and its
allocation-free `FST.Reader` is documented as single-threaded. To keep the
transducer walk allocation-free **and** satisfy the concurrent-`MatchQuery`
contract, the reader keeps a `sync.Pool` of `*vellum.Reader`: each query borrows a
reader (which carries a reusable prealloc state it clears and refills on every
`Get`), then returns it. No mutable state is shared across goroutines, and the
per-query state allocation is removed. If a pooled reader is unavailable it falls
back to the allocating `FST.Get`.

Keys are passed to `Get` via a zero-copy `unsafe.Slice` over the query string's
bytes (safe because `Get` treats the key as read-only and never retains it).

## On-disk layout

Unlike the default container, fst_dict writes **no FlatDict block** — the FST *is*
the term dictionary. Two blocks are written per field:

1. **Postings block** (`BlockKindPostings`): concatenated
   [`FlatPostingList`](../../../segment/posting_list.go)s, EntryIDs ascending.
2. **FST block** (`<field>_fst_dict`): the serialized vellum transducer.

```
FST value per term
┌────────────────────┐
│ offset (u64)       │   block-relative byte offset of the term's
└────────────────────┘   FlatPostingList header in the postings block
```

The FST value is a **single `uint64` offset** — no `PostingRef` payload. The
posting **count** is recovered by the reader from the `FlatPostingList` header at
that offset, so nothing is duplicated. `vellum.Load` maps the block zero-copy; the
slice is owned by the `SegmentReader` and outlives the reader.

## Example

```go
fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
    "url": {
        ID:    1,
        Field: "url",
        FieldOption: be_indexer.FieldOption{
            IndexType: "fst_dict",
            Encoder:   "default",
        },
    },
}
```

## fst_dict vs mph_dict

| | fst_dict | mph_dict |
|:--|:---------|:---------|
| Lookup | `O(len(term))`, vocab-independent | `O(1)` |
| Stores terms | Yes (ordered, prefix-compressed) | No |
| Enumerable / prefix scan | Yes | No |
| Best key shape | structured, shared affixes | opaque, high-entropy |
| Compression | folds shared prefixes/suffixes | value table only |
| Verifies key presence | Yes | No (see mph_dict safety note) |

See the [index overview](README.md) for the full comparison.
