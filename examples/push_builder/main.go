// Command push_builder demonstrates the push-model directory builders
// (FullIndexBuilder / DeltaIndexBuilder) end to end:
//
//  1. Read a "full snapshot" data file and push each line as a Document into a
//     FullIndexBuilder, committing a full index generation directory.
//  2. Read a "delta" data file and push each line as a Mutation into a
//     DeltaIndexBuilder, committing a delta index generation directory.
//  3. Publish a snapshot manifest referencing full + delta.
//  4. Load the snapshot with loader.OpenIndex and strictly verify retrieval
//     results against the expected post-merge state.
//
// The data files use a tiny line format so the example is self-contained:
//
//	full.txt   : "<docID> <city> <age>"                 -> upsert-like full doc
//	delta.txt  : "<op> <docID> <version> [city age]"     op = upsert|delete
//
// Run:
//
//	go run ./examples/push_builder
//
// It writes the index under a temp dir and prints the verification summary.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

const schemaHash = "sha256:push-builder-example"

// fields defines the index schema: two exact-match numeric fields.
func fields() map[core.BEField]*core.FieldMeta {
	return map[core.BEField]*core.FieldMeta{
		"city": {ID: 1, Field: "city", FieldOption: core.FieldOption{Encoder: "number"}},
		"age":  {ID: 2, Field: "age", FieldOption: core.FieldOption{Encoder: "number"}},
	}
}

// fullRecord is one line of the full snapshot file.
type fullRecord struct {
	docID core.DocID
	city  int
	age   int
}

// deltaRecord is one line of the delta file.
type deltaRecord struct {
	op      string // upsert | delete
	docID   core.DocID
	version uint64
	city    int
	age     int
}

func main() {
	root, err := os.MkdirTemp("", "push-builder-example-*")
	if err != nil {
		fail("mkdir temp: %v", err)
	}
	fmt.Printf("index root: %s\n", root)

	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fail("mkdir data: %v", err)
	}
	fullPath := filepath.Join(dataDir, "full.txt")
	deltaPath := filepath.Join(dataDir, "delta.txt")
	writeSampleData(fullPath, deltaPath)

	// ---- 1. Build the full index by pushing documents one at a time. ----
	fullDesc := buildFullIndex(root, fullPath)
	fmt.Printf("full built: gen=%d segments=%d\n", fullDesc.Generation, len(fullDesc.Segments))

	// ---- 2. Build the delta index by pushing mutations one at a time. ----
	deltaDesc := buildDeltaIndex(root, deltaPath)
	fmt.Printf("delta built: gen=%d changed=%d deleted=%d\n",
		deltaDesc.Generation, deltaDesc.ChangedDocCount, deltaDesc.DeletedDocCount)

	// ---- 3. Publish a snapshot manifest (serving-side helper). ----
	m, err := builder.NewSnapshotManifest(builder.SnapshotManifestRequest{
		IndexName:  "push-builder-example",
		Generation: deltaDesc.Generation,
		SchemaHash: schemaHash,
		Full:       fullDesc,
		Deltas:     []manifest.DeltaIndexDescriptor{deltaDesc},
	})
	if err != nil {
		fail("NewSnapshotManifest: %v", err)
	}
	if err := manifest.PublishManifest(root, "manifest-000002.json", m); err != nil {
		fail("PublishManifest: %v", err)
	}

	// ---- 4. Load and strictly verify. ----
	verify(root)

	fmt.Println("OK: all retrieval assertions passed")
}

func buildFullIndex(root, fullPath string) manifest.FullIndexDescriptor {
	b, err := builder.NewFullIndexBuilder(builder.FullIndexBuildOption{
		Root:              root,
		Generation:        1,
		SnapshotWatermark: 100,
		Fields:            fields(),
		// Force multiple internal segments to exercise segment rolling.
		Options:  builder.BuildDirectoryOptions{MaxDocsPerSegment: 2, SegmentSchemaHash: schemaHash},
		FailMode: builder.FailFast,
	})
	if err != nil {
		fail("NewFullIndexBuilder: %v", err)
	}
	// defer Close() is always safe: it aborts+cleans tmp before a successful
	// Build, and is a no-op afterwards.
	defer b.Close()

	for _, rec := range readFullFile(fullPath) {
		doc := core.NewDocument(rec.docID).AddConjunction(
			core.NewConjunction().In("city", rec.city).In("age", rec.age),
		)
		if err := b.AddDocument(doc); err != nil {
			fail("AddDocument(%d): %v", rec.docID, err)
		}
	}

	desc, err := b.Build()
	if err != nil {
		fail("full Build: %v", err)
	}
	// Descriptor is self-describing & verifiable: check each segment checksum.
	for _, seg := range desc.Segments {
		size, checksum, err := manifest.SHA256File(filepath.Join(root, desc.Path, seg.File))
		if err != nil {
			fail("checksum full segment: %v", err)
		}
		if size != seg.Size || checksum != seg.Checksum {
			fail("full segment %s descriptor does not match file", seg.File)
		}
	}
	return desc
}

func buildDeltaIndex(root, deltaPath string) manifest.DeltaIndexDescriptor {
	b, err := builder.NewDeltaIndexBuilder(builder.DeltaIndexBuildOption{
		Root:                   root,
		Generation:             2,
		FromWatermarkExclusive: 100,
		ToWatermarkInclusive:   110,
		Fields:                 fields(),
		Options:                builder.BuildDirectoryOptions{SegmentSchemaHash: schemaHash},
		FailMode:               builder.FailFast,
		// MaxMutations: 0 => unlimited (default).
	})
	if err != nil {
		fail("NewDeltaIndexBuilder: %v", err)
	}
	defer b.Close()

	for _, rec := range readDeltaFile(deltaPath) {
		var m builder.Mutation
		switch rec.op {
		case "upsert":
			doc := core.NewDocument(rec.docID).AddConjunction(
				core.NewConjunction().In("city", rec.city).In("age", rec.age),
			)
			m = builder.Mutation{DocID: rec.docID, Version: rec.version, Op: builder.MutationUpsert, Document: doc}
		case "delete":
			m = builder.Mutation{DocID: rec.docID, Version: rec.version, Op: builder.MutationDelete}
		default:
			fail("unknown delta op %q", rec.op)
		}
		if err := b.AddMutation(m); err != nil {
			fail("AddMutation(%d): %v", rec.docID, err)
		}
	}

	desc, err := b.Build()
	if err != nil {
		fail("delta Build: %v", err)
	}
	return desc
}

// verify loads the published snapshot and asserts the merged (full+delta) state.
//
// Each document is a conjunction of TWO include predicates (city AND age), i.e.
// K=2, so a query must supply BOTH fields to match — this is the library's
// K-Groups AND semantics.
//
// Full snapshot:
//
//	doc 1: city=1 age=20
//	doc 2: city=1 age=30
//	doc 3: city=2 age=20
//	doc 4: city=3 age=40
//
// Delta:
//
//	upsert doc 2 -> city=2 age=30   (moves doc 2 from city 1 to city 2)
//	delete doc 4                     (removes doc 4)
//	delete then re-upsert doc 5 -> city=1 age=25 (highest version wins: upsert)
//
// Expected merged state (city, age):
//
//	doc 1: (1, 20)   unchanged
//	doc 2: (2, 30)   moved by delta
//	doc 3: (2, 20)   unchanged
//	doc 5: (1, 25)   re-created by delta (upsert v2 beats delete v1)
//	doc 4: deleted
func verify(root string) {
	ce, err := loader.OpenIndex(root, fields(), loader.Options{SchemaHash: schemaHash})
	if err != nil {
		fail("OpenIndex: %v", err)
	}
	defer ce.Close()

	// Full-only, unchanged docs.
	assertQuery(ce, core.Assignments{"city": 1, "age": 20}, 1) // doc 1
	assertQuery(ce, core.Assignments{"city": 2, "age": 20}, 3) // doc 3
	// Delta upsert moved doc 2 to (2,30); its old (1,30) must no longer match.
	assertQuery(ce, core.Assignments{"city": 2, "age": 30}, 2)
	assertQuery(ce, core.Assignments{"city": 1, "age": 30}) // superseded, empty
	// Delta delete removed doc 4.
	assertQuery(ce, core.Assignments{"city": 3, "age": 40})
	// Delete-then-upsert: highest version (upsert) wins, doc 5 present at (1,25).
	assertQuery(ce, core.Assignments{"city": 1, "age": 25}, 5)
}

func assertQuery(ce interface {
	Retrieve(core.Assignments, ...core.IndexOpt) (*core.BitmapDocSet, error)
}, q core.Assignments, want ...core.DocID) {
	b, err := ce.Retrieve(q)
	if err != nil {
		fail("Retrieve %v: %v", q, err)
	}
	var got core.DocIDList
	b.ForEach(func(id core.DocID) { got = append(got, id) })
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		fail("query %v: got %v want %v", q, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			fail("query %v: got %v want %v", q, got, want)
		}
	}
	fmt.Printf("  query %v -> %v\n", q, got)
}

// --------------------------------------------------------------------------------
// Sample data + tiny line-format parsers.
// --------------------------------------------------------------------------------

func writeSampleData(fullPath, deltaPath string) {
	full := strings.Join([]string{
		"1 1 20",
		"2 1 30",
		"3 2 20",
		"4 3 40",
	}, "\n") + "\n"
	if err := os.WriteFile(fullPath, []byte(full), 0o644); err != nil {
		fail("write full data: %v", err)
	}

	// op docID version [city age]
	delta := strings.Join([]string{
		"upsert 2 1 2 30", // move doc 2 to city 2
		"delete 4 1",      // remove doc 4
		"delete 5 1",      // doc 5 delete (older version)
		"upsert 5 2 1 25", // doc 5 re-upsert (newer version wins)
	}, "\n") + "\n"
	if err := os.WriteFile(deltaPath, []byte(delta), 0o644); err != nil {
		fail("write delta data: %v", err)
	}
}

func readFullFile(path string) []fullRecord {
	var out []fullRecord
	eachLine(path, func(f []string) {
		if len(f) != 3 {
			fail("full line needs 3 fields, got %v", f)
		}
		out = append(out, fullRecord{
			docID: core.DocID(atoi(f[0])),
			city:  atoi(f[1]),
			age:   atoi(f[2]),
		})
	})
	return out
}

func readDeltaFile(path string) []deltaRecord {
	var out []deltaRecord
	eachLine(path, func(f []string) {
		if len(f) < 3 {
			fail("delta line needs >=3 fields, got %v", f)
		}
		rec := deltaRecord{
			op:      f[0],
			docID:   core.DocID(atoi(f[1])),
			version: uint64(atoi(f[2])),
		}
		if rec.op == "upsert" {
			if len(f) != 5 {
				fail("upsert delta line needs 5 fields, got %v", f)
			}
			rec.city = atoi(f[3])
			rec.age = atoi(f[4])
		}
		out = append(out, rec)
	})
	return out
}

func eachLine(path string, fn func([]string)) {
	f, err := os.Open(path)
	if err != nil {
		fail("open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fn(strings.Fields(line))
	}
	if err := sc.Err(); err != nil {
		fail("scan %s: %v", path, err)
	}
}

func atoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		fail("atoi %q: %v", s, err)
	}
	return v
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
