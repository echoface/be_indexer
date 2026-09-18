# Unify `InMemorySegmentBuilder` / `ExternalBuilder` into one `SegmentBuilder`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the duplicated segment-construction framework by merging `segment.InMemorySegmentBuilder` and `segment.ExternalBuilder` into a single `segment.SegmentBuilder` parameterized by `SegmentBuilderOptions`, with **byte-identical** on-disk output for every existing build path.

**Architecture:** The two builders are the same framework (block alignment, checksums, MetaBlock/footer assembly, per-field `IndexBuilder` dispatch) with exactly two axes of difference: (1) the `BuilderEnv` handed to `IndexBuilder` (`MaxPostingsInMemory` / `TmpDir` = spill on/off), and (2) the source of the `__wildcards` block (in-memory `core.Entries` vs a pre-sorted sidecar streamed to disk). Both are expressible as options, so they collapse into one implementation. The on-disk Segment v5 format, the reader, the loader, the manifest, and the `builder` package's Full/Delta semantics are untouched.

## Global Constraints

- No new `go.mod` dependencies; Go 1.24, `CGO_ENABLED=0`.
- Two-level file changes must keep the Segment v5 byte layout **identical**; a byte change would silently break already-published indexes without a format-version bump. Tests must enforce byte-equality.
- No heap allocations or behavior changes in the retrieve hot path (this refactor does not touch `engine`/`segment` read paths).
- No panic on the build error paths; all errors returned.
- Tests use GoConvey where the package already does; `segment` tests use std `testing`.
- Commands: build = `make build`, test = `make test`, vet = `go vet ./...`.

## Background (evidence)

- `segment/segment_builder_mem.go:96` `Write()` and `segment/segment_builder_external.go:113` `Write()` are line-for-line equivalent apart from the wildcard-source branch and receiver names; the same holds for `writeBytes`/`alignTo8`/`beginBlock`/`finishBlock`.
- `AddField` differs only in the env: `{MaxPostingsInMemory: 0, TmpDir: ""}` (`segment_builder_mem.go:77`) vs the spill env (`segment_builder_external.go:92`). Every registered `IndexBuilder` (`dict_index.go`, `range_builder.go`, `ac_builder.go`) consumes the env only through `NewKeyedPostingCollector`.
- `ExternalBuilder` additionally supports wildcard sidecar streaming (`SetWildcardsBlockFile` + `copyFrom`) and cleans up its runs dir after `Write`; `InMemorySegmentBuilder` has no capability `ExternalBuilder` lacks.
- Two dedicated parity tests exist only because of the duplication: `TestExternalBuilderMatchesBuilderAcrossRuns` (`segment/segment_builder_external_test.go:17`) and `TestExternalBuilderV5MatchesBuilderAcrossRuns` (`:165`).

## Non-goals

- No change to `FullIndexBuilder`/`DeltaIndexBuilder` input semantics (both are justified and stay).
- No change to `SegmentReader`, `FlatDict`/`FlatPostingList`, manifest, loader, or engine.
- No change to the spill policy itself (`MaxPostingsInMemory` still counts distinct keys, not postings — see `note.md` §4.2.1).

## Files Affected

| File | Action | Summary |
|------|--------|---------|
| `segment/segment_builder.go` | Add | Merged `SegmentBuilder` + `SegmentBuilderOptions` + `DefaultMaxPostingsInMemory` |
| `segment/segment_builder_mem.go` | Delete | Superseded; `NewInMemorySegmentBuilder` moves to the merged file as a wrapper |
| `segment/segment_builder_external.go` | Delete | Superseded |
| `builder/artifact.go` | Modify | `segmentBuild.esb` field type → `*segment.SegmentBuilder`; `startNewSegment` resolves the default spill threshold and handles the new constructor error |
| `builder/doc_exporter.go` | Modify | `writeSegmentFromDocsWithCodec` uses `NewSegmentBuilder`; `exportDocsToSegment` takes `postingSink` |
| `segment/segment_test.go` | Modify | Rename/repoint the `sink` interface + constructor call sites |
| `segment/segment_builder_external_test.go` | Modify | Repoint to `NewSegmentBuilder`; keep byte-parity + wildcard-streaming assertions (rename file to `segment_builder_test.go`) |
| `segment/posting_list_test.go`, `segment/segment_ac_test.go` | Modify | `NewInMemorySegmentBuilder` return type only (no call-site change needed if it stays `(w)` → `*SegmentBuilder`) |
| `AGENTS.md` | Modify | `segment` line + tree (`segment_builder.go`) |
| `note.md` | Modify | §2.1.2 build-path description |
| `architecture.html`, `static/docs/ARCHITECTURE.md` | Modify | Builder names / usage example |

---

### Task 1: Add the merged `segment.SegmentBuilder`

**Files:**
- Add: `segment/segment_builder.go`
- Consumes: `core.Entries`, `core.BEField`, `core.FieldOption`, `core.NormalizeFieldOption`, `encodeEntriesBlock`, `schemaHashFromFieldDumps`, `newSegmentBlockWriter`, `RegisterIndex`-backed `NewIndexBuilder`, `MagicNumber`, `MetaBlock`, `BlockDef`, `wildcardsBlockName`, `checksumPrefix`.
- Produces: `SegmentBuilderOptions`, `SegmentBuilder`, `NewSegmentBuilder`, `NewInMemorySegmentBuilder`, `DefaultMaxPostingsInMemory`.

- [x] **Step 1: Define the options and the default threshold**

```go
// DefaultMaxPostingsInMemory is the distinct-key spill threshold used by
// streaming callers that want the framework default. It is NOT applied
// implicitly: MaxPostingsInMemory <= 0 disables spilling, so a caller that
// wants bounded-memory streaming must resolve this default itself (or use
// segment.DefaultMaxPostingsInMemory).
const DefaultMaxPostingsInMemory = 1_000_000

// SegmentBuilderOptions configures a SegmentBuilder. The zero value builds the
// entire segment in memory with no spill and no wildcards — the exact behaviour
// of the former InMemorySegmentBuilder.
type SegmentBuilderOptions struct {
	// MaxPostingsInMemory is the distinct-key threshold at which a field
	// collector spills a sorted run to TmpDir. <= 0 disables spill entirely
	// (in-memory only). When > 0, TmpDir must be non-empty.
	MaxPostingsInMemory int
	// TmpDir is the directory for spill runs. Ignored when
	// MaxPostingsInMemory <= 0.
	TmpDir string
	// Wildcards are the K=0 entries encoded into the __wildcards block.
	Wildcards core.Entries
	// WildcardsBlockFile, when non-empty, is a pre-sorted sidecar in
	// encodeEntriesBlock format that is streamed into the __wildcards block
	// instead of Wildcards. It takes precedence over Wildcards. The file is
	// created by the caller after construction, so it is not validated here.
	WildcardsBlockFile string
}
```

- [x] **Step 2: Define the type and constructors**

```go
type SegmentBuilder struct {
	docCount int
	fields   map[string]FieldMetaDump
	builders map[string]IndexBuilder

	w      io.Writer
	offset uint64

	maxRecs            int
	tmpDir             string
	wildcards          core.Entries
	wildcardsBlockFile string

	blockChecksums map[string]string
	curChecksum    hash.Hash
}

// NewSegmentBuilder creates a segment builder. It fails only on an invalid
// option combination: spill enabled without a temp dir.
func NewSegmentBuilder(w io.Writer, opts SegmentBuilderOptions) (*SegmentBuilder, error) {
	if opts.MaxPostingsInMemory > 0 && opts.TmpDir == "" {
		return nil, fmt.Errorf("segment builder: TmpDir is required when MaxPostingsInMemory > 0")
	}
	return &SegmentBuilder{
		fields:             make(map[string]FieldMetaDump),
		builders:           make(map[string]IndexBuilder),
		w:                  w,
		maxRecs:            opts.MaxPostingsInMemory,
		tmpDir:             opts.TmpDir,
		wildcards:          append(core.Entries(nil), opts.Wildcards...),
		wildcardsBlockFile: opts.WildcardsBlockFile,
		blockChecksums:     make(map[string]string),
	}, nil
}

// NewInMemorySegmentBuilder is the zero-spill convenience constructor,
// equivalent to NewSegmentBuilder(w, SegmentBuilderOptions{}) and unable to fail.
func NewInMemorySegmentBuilder(w io.Writer) *SegmentBuilder {
	b, _ := NewSegmentBuilder(w, SegmentBuilderOptions{})
	return b
}
```

- [x] **Step 3: Port the shared mutators (identical to the old pair)**

`SetDocCount`, `SetWildcards` (copies entries, clears `wildcardsBlockFile`), `SetWildcardsBlockFile` (sets path, clears `wildcards`), `AddField` (duplicate-field error, `FieldMetaDump`, `NewIndexBuilder(normalized.IndexType, BuilderEnv{MaxPostingsInMemory: b.maxRecs, TmpDir: b.tmpDir})`), `AddRecord` (delegates by field name).

- [x] **Step 4: Port `Write` as a single implementation**

```go
func (b *SegmentBuilder) Write() error {
	defer b.cleanupRuns() // guarded: tmpDir may be "" for in-memory builds
	if err := b.writeBytes(MagicNumber); err != nil {
		return err
	}

	blockIndex := make(map[string]BlockDef)
	fields := make([]string, 0, len(b.fields))
	for f := range b.fields {
		fields = append(fields, f)
	}
	sort.Strings(fields)

	for _, field := range fields {
		bw := newSegmentBlockWriter(field, b.w, &b.offset, blockIndex, b.blockChecksums)
		if err := b.builders[field].Build(bw); err != nil {
			return fmt.Errorf("field %s build failed: %w", field, err)
		}
	}

	if err := b.writeWildcardsBlock(blockIndex); err != nil {
		return err
	}

	meta := MetaBlock{
		Version:        SegmentVersionV5,
		DocCount:       b.docCount,
		BlockIndex:     blockIndex,
		WildcardsBlock: wildcardsBlockName,
	}
	for _, field := range fields {
		meta.Fields = append(meta.Fields, b.fields[field])
	}
	schemaHash, err := schemaHashFromFieldDumps(meta.Fields)
	if err != nil {
		return err
	}
	meta.SchemaHash = schemaHash

	// Fold field-block checksums (registered by segmentBlockWriter) into the
	// BlockDef entries. The wildcards BlockDef already carries its checksum.
	for name, sum := range b.blockChecksums {
		if def, ok := blockIndex[name]; ok {
			def.Checksum = sum
			blockIndex[name] = def
		}
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	metaOffset := b.offset
	if err := b.writeBytes(metaBytes); err != nil {
		return err
	}
	footer := make([]byte, 8)
	binary.LittleEndian.PutUint64(footer, metaOffset)
	return b.writeBytes(footer)
}
```

- [x] **Step 5: Port the wildcard block (both sources) and helpers**

`writeWildcardsBlock` aligns to 8, records `off`, `beginBlock()`, then either streams `wildcardsBlockFile` via `copyFromFile` or sorts a copy of `wildcards` and writes `encodeEntriesBlock`; on success `finishBlock()` returns the checksum and the `BlockDef{Kind: BlockKindWildcards, Offset, Size, Checksum}` is registered. `copyFromFile` streams in 64 KiB chunks through `writeBytes` (offset + checksum stay in sync). `cleanupRuns` removes `tmpDir` only when non-empty.

---

### Task 2: Migrate callers

**Files:**
- Modify: `builder/artifact.go:170,390-406`
- Modify: `builder/doc_exporter.go:134,157`

- [x] **Step 1: `FullIndexBuilder.startNewSegment` (`builder/artifact.go:380`)**

Resolve the streaming default explicitly, since `MaxPostingsInMemory <= 0` now means "no spill":

```go
maxRecs := b.opt.Options.MaxPostingsInMemory
if maxRecs <= 0 {
	maxRecs = segment.DefaultMaxPostingsInMemory
}
sb, err := segment.NewSegmentBuilder(tmp, segment.SegmentBuilderOptions{
	MaxPostingsInMemory: maxRecs,
	TmpDir:              filepath.Join(b.tmpDir, ".runs", fmt.Sprintf("segment-%06d", b.segIdx)),
})
if err != nil {
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	wildAcc.Cleanup()
	return err
}
```

- [x] **Step 2: Rename the `segmentBuild.esb` field to `sb *segment.SegmentBuilder`** and update `:261`, `:397`, `:405`, `:431`, `:438`, `:440` accordingly.

- [x] **Step 3: `builder/doc_exporter.go`** — replace `segment.NewInMemorySegmentBuilderWithOptions(w, segment.InMemorySegmentBuilderOptions{})` with `segment.NewInMemorySegmentBuilder(w)`, and change `exportDocsToSegment(sw *segment.InMemorySegmentBuilder, …)` to take the existing `postingSink` interface (only `AddRecord` is used).

---

### Task 3: Migrate and strengthen tests

**Files:**
- Modify/rename: `segment/segment_builder_external_test.go` → `segment/segment_builder_test.go`
- Modify: `segment/segment_test.go`, `segment/posting_list_test.go`, `segment/segment_ac_test.go`

- [x] **Step 1: Repoint constructors.** `NewExternalBuilder(buf, tmp, ExternalBuilderOptions{MaxPostingsInMemory: n, …})` → `NewSegmentBuilder(buf, SegmentBuilderOptions{MaxPostingsInMemory: n, TmpDir: tmp, …})`; `NewInMemorySegmentBuilderWithOptions(buf, InMemorySegmentBuilderOptions{Wildcards: x})` → `NewSegmentBuilder(buf, SegmentBuilderOptions{Wildcards: x})`.

- [x] **Step 2: Keep the byte-parity tests** (now proving option handling): in-memory (`MaxPostingsInMemory: 0`) vs forced spill (`: 1`/`: 2`) must produce identical bytes, and the wildcard block-file path must equal the in-memory wildcard path.

- [x] **Step 3: Add spill-plumbing assertions** (new, guards the resolved default):
  - after adding more distinct keys than the threshold (before `Write`), at least one `kpc-run-*.bin` exists under `TmpDir`;
  - after `Write`, `TmpDir` has been removed;
  - `NewSegmentBuilder(w, SegmentBuilderOptions{MaxPostingsInMemory: 1})` returns an error.

- [x] **Step 4:** Rebuild the reader in the parity test (`NewSegmentReader`) so the byte-identity claim is validated against the real verifier (block checksums), not just `bytes.Equal`.

---

### Task 4: Docs

- [x] `AGENTS.md:53` → `SegmentBuilder`(统一，按 options 决定溢写) ; tree lines `:83-84` → one `segment_builder.go`.
- [x] `note.md:54-55` → single builder, two option profiles.
- [x] `architecture.html:129`, `static/docs/ARCHITECTURE.md:172-175,444-447` → new names/example.

---

## Self-Review (correctness & compatibility)

### Invariants that must hold, and how the merge preserves them

| Invariant | Preserved by | Guard |
|---|---|---|
| Segment v5 bytes identical for identical input | one `Write()`, same statement order (magic → fields → wildcards → schema hash → checksum fold → meta → footer) | byte-parity tests + `NewSegmentReader` checksum verification |
| Every block has a sha256 checksum, ≥1 block exists | wildcards block is always written (even empty ⇒ 16-byte `BEIENT1` header) and registered with checksum | `verifyBlockChecksumMetadata` in `NewSegmentReader` |
| 8-byte alignment before every block | unchanged `segmentBlockWriter`/`alignTo8` | existing segment tests |
| Deterministic output | `sort.Strings(fields)`, `sort` of copied wildcards, `json.Marshal` of a map (Go sorts keys) | byte-parity tests |
| In-memory path never spills | `NewInMemorySegmentBuilder` ⇒ `MaxPostingsInMemory = 0` ⇒ collector `maxRecs = 1<<30` | existing in-memory tests + no tmp dir required |
| Streaming path keeps bounded memory | explicit `DefaultMaxPostingsInMemory` resolution in `FullIndexBuilder`; `TmpDir` required when spill on | new spill-file assertion |
| No writes to or removal of an empty tmp path | `cleanupRuns`/`copyFromFile` guard `tmpDir != ""` | code review; `os.RemoveAll("")` is never reached |
| In-memory branch does not mutate caller's wildcard slice | `append(core.Entries(nil), b.wildcards...)` before sort | existing wildcard tests |

### Adversarial review (Q → finding)

- **Does merging change peak memory for the in-memory path?** No. Both old paths already delegated to the same `IndexBuilder`s; the only difference was the collector threshold. With `0` the collector keeps the old `1<<30` cap.
- **Could the spill default silently regress (full builds become in-memory)?** Only if the default resolution moves incorrectly. Mitigation: the default is resolved once in `startNewSegment` and asserted by the new "runs exist while building" test. Note this is a real hazard: spill-vs-not is byte-identical, so parity tests alone would NOT catch it.
- **Could two builders have differed in some undocumented way?** Reviewed field-by-field: `AddField`, `AddRecord`, `SetDocCount`, wildcard setters, and all write helpers are equivalent; the env and wildcard source were the only axes. No other capability exists.
- **Does moving `TmpDir` into options break the `FullIndexBuilder` runs-dir lifecycle?** No: it still passes a per-segment dir under `b.tmpDir`; `Write`'s deferred cleanup removes exactly that dir, and `Close`/`fatal` still `RemoveAll(b.tmpDir)`.
- **Is constructor validation of `TmpDir` a behavior change?** Yes but a safe one: today `KeyedPostingCollector.flushRun` silently returns without flushing when `tmpDir == ""` (`segment/collector.go:70-72`), i.e. spill would silently degrade to unbounded memory. Failing fast at construction is strictly better and no in-repo caller hits it.
- **Segment version bump needed?** No — output is byte-identical, so old readers/loaders load new-built segments and vice versa. If any byte difference is ever observed, that is a bug, not a reason to bump.
- **Is `NewInMemorySegmentBuilder` allowed to keep swallowing the constructor result?** Yes: with `MaxPostingsInMemory == 0` the validation cannot trigger, so `_` is provably safe. `NewSegmentBuilder` is the only fallible entry point.
- **Concurrency/regression scope?** Builders are single-use and not concurrent; nothing in `engine`/`loader` imports these types, so the blast radius is `segment` + `builder` + their tests.

### Compatibility matrix

| Surface | Impact | Action |
|---|---|---|
| Segment v5 on-disk format | none (byte-identical) | keep `SegmentVersionV5` |
| `SegmentReader`/`FlatDict`/`FlatPostingList` | none | untouched |
| Manifest / CURRENT / loader / engine | none | untouched |
| Root package `be_indexer.go` exports | none (does not re-export segment builders) | untouched |
| `segment.NewExternalBuilder`, `ExternalBuilder`, `ExternalBuilderOptions` | **removed** (API break, allowed) | migrate 1 caller + tests |
| `segment.InMemorySegmentBuilder` type, `NewInMemorySegmentBuilderWithOptions`, `InMemorySegmentBuilderOptions` | **removed** (API break, allowed) | `NewInMemorySegmentBuilder(w)` kept as convenience; `WithOptions` call sites become `NewSegmentBuilder(w, SegmentBuilderOptions{Wildcards: …})` |
| `segment.SegmentBuilder` (new) | added | — |

### Risks & mitigations

1. **Spill default regression** (highest): explicit resolution at the call site + new "spill file exists during build / runs dir removed after" test.
2. **Copy/paste drift during port**: mitigate by deleting both old files in the same change, so the compiler forces every call site through the new type; keep the byte-parity tests.
3. **Accidental `os.RemoveAll("")`**: unconditional guard on `tmpDir != ""`.
4. **Doc drift**: Task 4 lists every non-historical mention (`docs/geo-design-*` are historical design records and are left as-is).

### Verification commands

```bash
make build
go vet ./...
make test                                   # all packages
go test -run 'TestSegmentBuilder|TestExternal|TestInMemory' ./segment/...
go test ./builder/... ./loader/... ./engine/...
go run ./examples/push_builder              # full+delta end-to-end
```

Acceptance: all existing tests pass unmodified in intent, the byte-parity tests pass, the new spill assertions pass, and `git grep -n 'ExternalBuilder\|InMemorySegmentBuilderWithOptions'` returns only historical docs.

---

## Implementation Status — DONE, verified

All tasks above are implemented. Results:

- `segment/segment_builder.go` is the single builder; `segment_builder_mem.go` / `segment_builder_external.go` deleted.
- Callers migrated: `builder/artifact.go` (`segmentBuild.sb *segment.SegmentBuilder`, explicit `DefaultMaxPostingsInMemory` resolution + constructor-error cleanup) and `builder/doc_exporter.go` (`NewInMemorySegmentBuilder` + `postingSink`).
- Tests: `segment/segment_builder_test.go` holds the migrated parity tests plus new `TestSegmentBuilderSpillsAndCleansRuns` (runs really hit disk, dir removed after `Write`) and `TestNewSegmentBuilderRejectsSpillWithoutTmpDir`. `TestBuilderRangeRoundTrip` now exercises a real spill threshold instead of a never-triggered one.
- Docs synced: `AGENTS.md`, `note.md` (§2.1.2, §4.2.1, §4.2.2), `architecture.html`, `static/docs/ARCHITECTURE.md`.

### Production verification

1. `go build ./...` and `go vet ./...` pass.
2. `go test ./segment/... ./builder/... ./loader/... ./engine/...` pass.
3. **Pre/post byte-identity proof.** A golden harness was run in a `git archive HEAD` copy of the pre-refactor tree and in the refactored tree, building six segment scenarios (dict in-memory, dict spill, multi-field + in-memory wildcards, streamed wildcard sidecar, `ext_range` spill, `ac_matcher` spill). All six outputs are byte-identical (`cmp` clean), proving the on-disk Segment v5 format did not change and existing published indexes remain loadable.
4. The stored `SegmentVersionV5` is unchanged, consistent with (3).
