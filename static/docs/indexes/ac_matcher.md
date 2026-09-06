# ac_matcher Index (Aho-Corasick)

`IndexType: "ac_matcher"`. A **multi-pattern** matcher. The field's indexed
values are treated as patterns compiled into a single **Aho-Corasick automaton**,
stored as a **Double-Array Trie (DAT)**. At query time one linear pass over the
input text finds *every* indexed pattern that occurs in it — all patterns scored
simultaneously.

- Source: [`segment/ac_index.go`](../../../segment/ac_index.go),
  [`segment/ac_builder.go`](../../../segment/ac_builder.go)
- Registered as: `core.IndexNameACMatcher`

## When to use

- **"Does this text contain any of my keywords?"** screening: sensitive-word
  filtering, brand/keyword targeting, tag extraction, routing by substring.
- You have **many patterns** and want to test them all in a single pass instead
  of N separate lookups.
- The predicate semantics are **substring containment**, not exact equality.

## When NOT to use

- **Exact equality** on a whole value — use [`default`](default.md),
  [`mph_dict`](mph_dict.md), or [`fst_dict`](fst_dict.md). AC matches substrings
  anywhere in the input, which is more work and different semantics.
- **Numeric ranges** — use [`ext_range`](ext_range.md).
- Patterns change very frequently at fine granularity — the DAT is built offline;
  rebuilding the automaton is an offline-segment cost.

## Retrieval complexity

| Operation | Cost |
|:----------|:-----|
| Query (match all patterns) | `O(len(text))` — one pass, plus output reporting |
| Posting scan / merge | `O(posting length)` per matched pattern |
| Build | `O(total pattern length)` to build trie + fail links + DAT layout |

Query time is driven by the **input text length**, essentially independent of the
number of patterns — the core advantage of Aho-Corasick. Transitions are resolved
by `base[state] + symbolID` array indexing (see the symbol-table note below).

## How it works

- Every rune appearing in any pattern is mapped to a **dense symbol id** in
  `[1, symCount]`; `0` is reserved as "unknown" so an unseen input rune always
  fails cleanly. This keeps the DAT address space proportional to the alphabet
  actually used, not the full Unicode range.
- Transitions use `base[state] + symbolID` (not `+ rune`), with a 64-bit
  intermediate so `base+sym` cannot overflow and forge a spurious transition.
- **Fail links** are precomputed and flattened; output sets are merged along fail
  links at build time so each terminal state directly lists all patterns ending
  there.

## On-disk layout

Index block `<field>_ac_matcher`:

```
Header (12 bytes)
┌──────────────┬──────────────┬──────────────┐
│ stateCount   │ symCount     │ reserved     │
│  (u32)       │  (u32)       │  (u32 = 0)   │
└──────────────┴──────────────┴──────────────┘

Symbol table            symCount × u32   (dense id → rune)

Parallel state arrays   stateCount × u32 each, in order:
  base[]        transition base offsets
  check[]       owner-state guard for base+sym slots
  fail[]        fail-link target state
  outputPtrs[]  offset into outputData (+1; 0 = no output)

Output data (variable)  per terminal state:
  [count u16] then count × { postingOffset u64, postingCount u32 }
```

The `base/check/fail/outputPtrs` arrays are read zero-copy via `unsafe.Slice`
over the mapped bytes. Output payloads carry both the posting offset and count,
so a matched pattern yields a posting cursor without a second header read. The
posting lists live in the field's separate postings block as
[`FlatPostingList`](../../../segment/posting_list.go)s.

## Example

```go
fields := be_indexer.Schema{
    "content": {IndexType: be_indexer.IndexNameACMatcher, Encoder: "default"},
}
// Query with the full text; every indexed pattern occurring in it matches.
```

See the [index overview](README.md) for how this compares to the other
containers.
