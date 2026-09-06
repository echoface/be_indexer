# Default Index (FlatDict)

`IndexType: "default"` (or empty). The baseline exact-match container. A field's
distinct terms are stored **sorted** in a flat array; lookup is a binary search
that resolves a term to its posting-list offset.

- Source: [`segment/dict_index.go`](../../../segment/dict_index.go),
  [`segment/flatmap.go`](../../../segment/flatmap.go)
- Registered as: `core.IndexNameDefault`

## When to use

- **Exact-match predicates**: `EQ`, `IN`, `NOT IN` on strings or numbers.
- Small to medium vocabularies (rule of thumb: up to ~1M distinct terms per
  field). Binary search stays cache-friendly and the term bytes are cheap.
- You want the terms to remain **enumerable** on disk (debugging, tooling,
  reverse lookups) — the raw term strings are stored verbatim.
- The default choice when in doubt: no external dependency, smallest build cost,
  deterministic layout.

## When NOT to use

- **Numeric range** queries (`age > 18`) — use [`ext_range`](ext_range.md). The
  default container only answers exact term equality.
- **Substring / multi-keyword** screening — use [`ac_matcher`](ac_matcher.md).
- Extremely large vocabularies where lookup latency or file size matters: if keys
  are opaque and high-entropy prefer [`mph_dict`](mph_dict.md) (`O(1)`, no term
  storage); if keys share structure (URLs, paths) prefer
  [`fst_dict`](fst_dict.md) (`O(len(term))`, strong prefix compression).

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Term lookup | `O(log n)` binary search over `n` sorted terms |
| Posting scan / merge | `O(posting length)` with galloping `SkipTo` |
| Build (sort terms) | `O(n log n)` |

Each comparison touches the item header (16 B) plus the term bytes, so cost also
scales mildly with term length. Vocabulary size drives both lookup depth and file
size (all term bytes are stored).

## On-disk layout

Index block `<field>_dict` (see `flatmap.go`):

```
FlatDict block
┌────────────────────┬────────────────────┐
│ Count (u32)        │ TermDataOffset (u32)│   header, 8 bytes
├────────────────────┴────────────────────┤
│ item[0]  … item[Count-1]                │   16 bytes each, sorted by term
│   ┌─────────────┬──────────────┬───────┐ │
│   │ KeyOffset u32│ PostingCount │ Post- │ │   KeyOffset: byte offset of term
│   │             │  u32         │ Offset│ │   PostingCount: EntryIDs in list
│   │             │              │  u64  │ │   PostingOffset: block-rel offset
│   └─────────────┴──────────────┴───────┘ │                 of posting header
├──────────────────────────────────────────┤
│ term string area (raw bytes, from        │   starts at TermDataOffset
│ TermDataOffset to end of block)           │   term length = next KeyOffset - this
└──────────────────────────────────────────┘
```

Notes:

- Items are sorted by term, enabling binary search directly over the mapped
  bytes.
- Carrying `PostingCount` in the item lets the reader construct a posting cursor
  without a second header read.
- A term's byte length is derived from the **next** item's `KeyOffset` (the last
  term runs to the end of the block).
- The posting lists themselves live in the field's separate postings block as
  [`FlatPostingList`](../../../segment/posting_list.go)s.

## Example

```go
fields := be_indexer.Schema{
    "city": {IndexType: be_indexer.IndexNameDefault, Encoder: "default"},
}
```

See the [index overview](README.md) for how this compares to the other
containers.
