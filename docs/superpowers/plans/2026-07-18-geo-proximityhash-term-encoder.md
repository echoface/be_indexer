# Geo Radius via proximityhash Covering Terms — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the custom geo container with a pure-term proximityhash covering scheme (per `docs/geo-design-v3.md`), while preserving — and re-proving via a new `container/example` template package — the pluggable container/encoder extension architecture.

**Architecture:** Build side expands `GeoParam{Lat,Lng,Radius}` into a deduped, sorted set of geohash covering cells (`proximityhash.CreateGeohash` + `CompressGeoHash`) emitted as plain `PostingKindTerm` postings → FlatDict/FlatPostingList. Query side expands a point into geohash prefixes of length 3..8 emitted as `QueryKindTerm` → existing `GetPostingsByTerm` path. Zero engine/segment changes. The extension framework (`segment.RegisterContainer`, `ContainerReader/Builder`, engine `default → ContainerQuery` branch, `parser.RegisterPredicateEncoder`) stays intact and gains a minimal out-of-package example (`container/example`) with e2e coverage.

**Tech Stack:** Go 1.24, `github.com/echoface/proximityhash` (already in go.mod), `github.com/mmcloughlin/geohash` (already in go.mod), GoConvey for tests.

## Global Constraints

- Approved design: `docs/geo-design-v3.md` + 3 approved deltas: (A) precision rule = coarsest precision with `cellWidth ≤ radius/2` over table `{3:156500, 4:39100, 5:4900, 6:1200, 7:152, 8:38}` (reproduces the doc's §5.1 reference table: 50km→5, 5km→6, 1km→7, 100m→8); (B) new `container/example` template package; (C) honest comments for the unwired `ContainerMetaBuilder`/`posting.Value` path.
- Encoder registration name is exactly `"proximitygeo"` (doc §10). NO `segment.RegisterContainer` call for geo.
- Do NOT modify `engine/`, `segment/` (except the Task 4 comment in `segment/container.go`), `parser/`, `core/`, `builder/` (except the Task 4 comment in `builder/doc_exporter.go`). Verify with `git diff --stat` before each commit.
- No new go.mod dependencies.
- Tests use GoConvey (`github.com/smartystreets/goconvey/convey`), test packages named `<pkg>_test`.
- All binary layouts use `binary.LittleEndian` explicitly (cross-platform segment files).
- No panics reachable from the retrieve path: encoder `Build`/`Query` return errors; the only panicking callees (`proximityhash.CreateGeohash` for precision 0/>12, `CompressGeoHash` for precision out of [1,12]) are called with compile-time-constant-bounded precisions in [3,8].
- Deterministic output: encoder Build output must be deduped AND sorted (`CompressGeoHash` returns map-ordered results; `CreateGeohash` emits duplicates).
- `DocID` range, K < 256 limits unchanged.
- Commands: test = `make test` (runs `go test ./...`), vet = `go vet ./...`.

---

### Task 1: Rewrite geo encoder (proximityhash terms) + delete custom container

**Files:**
- Rewrite: `container/geo/encoder.go`
- Delete: `container/geo/geo.go`
- Rewrite: `container/geo/geo_test.go` (unit + property tests; e2e comes in Task 2)

**Interfaces:**
- Consumes: `parser.EncodedPosting{Kind, Term}`, `parser.EncodedQuery{Kind, Term}`, `parser.PostingKindTerm`, `parser.QueryKindTerm`, `parser.RegisterPredicateEncoder(name, factory)`, `core.ValueExpr{Value}`, `util.DistinctString([]string) []string`, `proximityhash.CreateGeohash(lat, lng, radius float64, precision uint) []string`, `proximityhash.CompressGeoHash(codes []string, minPrecision, cutoffPrecision int) []string`, `geohash.EncodeWithPrecision(lat, lng float64, chars uint) string`.
- Produces (Task 2 and users rely on): `geo.GeoParam{Lat float64; Lng float64; Radius int}`, `geo.GeoQuery{Lat float64; Lng float64}`, `geo.Encoder` implementing `parser.PredicateEncoder`, registered under container name `"proximitygeo"`, exported const `geo.ContainerName = "proximitygeo"`.

- [ ] **Step 1: Delete the old container and write the new failing tests**

```bash
git rm container/geo/geo.go
```

Replace the entire content of `container/geo/geo_test.go` with:

```go
package geo_test

import (
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/mmcloughlin/geohash"
	"github.com/smartystreets/goconvey/convey"

	"github.com/echoface/be_indexer/container/geo"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

func buildCodes(t *testing.T, p geo.GeoParam) []string {
	t.Helper()
	expr := core.NewValueExpr(core.ValueOptEQ, p, true) // returns core.ValueExpr by value (core/boolean_expr.go:97)
	postings, err := geo.Encoder{}.Build(&expr)
	if err != nil {
		t.Fatalf("Build(%+v): %v", p, err)
	}
	codes := make([]string, 0, len(postings))
	for _, ep := range postings {
		if ep.Kind != parser.PostingKindTerm {
			t.Fatalf("expected PostingKindTerm, got %q", ep.Kind)
		}
		codes = append(codes, ep.Term)
	}
	return codes
}

// coveredBy reports whether the full-precision geohash of a point shares a
// prefix with at least one stored covering code.
func coveredBy(codes []string, lat, lng float64) bool {
	full := geohash.EncodeWithPrecision(lat, lng, 8)
	for _, c := range codes {
		if strings.HasPrefix(full, c) {
			return true
		}
	}
	return false
}

// offsetPoint moves (lat, lng) by dist meters at bearing theta radians.
func offsetPoint(lat, lng, dist, theta float64) (float64, float64) {
	const rEarth = 6371000.0
	dLat := (dist * math.Sin(theta) / rEarth) * (180 / math.Pi)
	dLng := (dist * math.Cos(theta) / rEarth) * (180 / math.Pi) / math.Cos(lat*math.Pi/180)
	return lat + dLat, lng + dLng
}

func TestPrecisionForRadius(t *testing.T) {
	convey.Convey("precision follows design table", t, func() {
		cases := []struct {
			radius int
			prec   int
		}{
			{1, 8}, {100, 8}, {1000, 7}, {5000, 6},
			{50000, 5}, {200000, 4}, {700000, 3},
		}
		for _, c := range cases {
			convey.So(geo.PrecisionForRadius(c.radius), convey.ShouldEqual, c.prec)
		}
	})
}

func TestEncoderBuild(t *testing.T) {
	bj := geo.GeoParam{Lat: 39.9042, Lng: 116.4074, Radius: 5000}

	convey.Convey("build produces deduped sorted covering terms", t, func() {
		codes := buildCodes(t, bj)
		convey.So(len(codes), convey.ShouldBeGreaterThan, 0)

		prec := geo.PrecisionForRadius(bj.Radius)
		seen := map[string]struct{}{}
		for i, c := range codes {
			convey.So(len(c), convey.ShouldBeBetweenOrEqual, 3, prec)
			if i > 0 {
				convey.So(codes[i-1] < c, convey.ShouldBeTrue) // sorted strictly asc == deduped
			}
			seen[c] = struct{}{}
		}
		convey.So(len(seen), convey.ShouldEqual, len(codes))

		convey.Convey("center point is covered", func() {
			convey.So(coveredBy(codes, bj.Lat, bj.Lng), convey.ShouldBeTrue)
		})

		convey.Convey("build is deterministic", func() {
			again := buildCodes(t, bj)
			convey.So(again, convey.ShouldResemble, codes)
		})
	})

	convey.Convey("build rejects bad input", t, func() {
		enc := geo.Encoder{}
		badTypeExpr := core.NewValueExpr(core.ValueOptEQ, "not-a-geoparam", true)
		_, err := enc.Build(&badTypeExpr)
		convey.So(err, convey.ShouldNotBeNil)
		_, err = enc.Build(nil)
		convey.So(err, convey.ShouldNotBeNil)

		bad := []geo.GeoParam{
			{Lat: 39.9, Lng: 116.4, Radius: 0},
			{Lat: 39.9, Lng: 116.4, Radius: -5},
			{Lat: 39.9, Lng: 116.4, Radius: 2_000_000},
			{Lat: 91, Lng: 116.4, Radius: 100},
			{Lat: 39.9, Lng: 181, Radius: 100},
			{Lat: math.NaN(), Lng: 116.4, Radius: 100},
		}
		for _, p := range bad {
			expr := core.NewValueExpr(core.ValueOptEQ, p, true)
			_, err := enc.Build(&expr)
			convey.So(err, convey.ShouldNotBeNil)
		}
	})
}

func TestEncoderQuery(t *testing.T) {
	convey.Convey("query expands to prefixes 3..8", t, func() {
		out, err := geo.Encoder{}.Query(geo.GeoQuery{Lat: 39.9042, Lng: 116.4074})
		convey.So(err, convey.ShouldBeNil)
		convey.So(len(out), convey.ShouldEqual, 6)

		full := geohash.EncodeWithPrecision(39.9042, 116.4074, 8)
		for i, q := range out {
			convey.So(q.Kind, convey.ShouldEqual, parser.QueryKindTerm)
			convey.So(q.Term, convey.ShouldEqual, full[:3+i])
		}
	})

	convey.Convey("query rejects bad input", t, func() {
		_, err := geo.Encoder{}.Query("nope")
		convey.So(err, convey.ShouldNotBeNil)
		_, err = geo.Encoder{}.Query(geo.GeoQuery{Lat: math.NaN(), Lng: 0})
		convey.So(err, convey.ShouldNotBeNil)
	})
}

// TestCoveringProperty is a pure encoder-level shadow test: any point well
// inside the circle must intersect the stored covering; any far point must not.
func TestCoveringProperty(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	centers := []geo.GeoParam{
		{Lat: 39.9042, Lng: 116.4074, Radius: 5000},
		{Lat: 31.2304, Lng: 121.4737, Radius: 1000},
		{Lat: -33.8688, Lng: 151.2093, Radius: 50000},
		{Lat: 48.8566, Lng: 2.3522, Radius: 300},
	}
	convey.Convey("covering property holds", t, func() {
		for _, p := range centers {
			codes := buildCodes(t, p)
			for i := 0; i < 200; i++ {
				dist := 0.8 * float64(p.Radius) * math.Sqrt(rnd.Float64())
				theta := rnd.Float64() * 2 * math.Pi
				lat, lng := offsetPoint(p.Lat, p.Lng, dist, theta)
				convey.So(coveredBy(codes, lat, lng), convey.ShouldBeTrue)
			}
			for i := 0; i < 50; i++ {
				dist := float64(p.Radius)*4 + 200000
				theta := rnd.Float64() * 2 * math.Pi
				lat, lng := offsetPoint(p.Lat, p.Lng, dist, theta)
				convey.So(coveredBy(codes, lat, lng), convey.ShouldBeFalse)
			}
		}
	})
}

func TestRegistration(t *testing.T) {
	convey.Convey("proximitygeo registers encoder only, no container", t, func() {
		enc, err := parser.NewPredicateEncoder(core.FieldMeta{
			Field:       "loc",
			FieldOption: core.FieldOption{Container: geo.ContainerName},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(enc, convey.ShouldNotBeNil)
		convey.So(segment.HasContainer(geo.ContainerName), convey.ShouldBeFalse)
	})
}
```

Note: `core.NewValueExpr(op, value, incl)` is at `core/boolean_expr.go:97` and returns `ValueExpr` by value; `Encoder.Build` takes `*core.ValueExpr`, hence the `&expr` pattern above.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./container/geo/ -run 'TestPrecisionForRadius|TestEncoderBuild|TestEncoderQuery|TestCoveringProperty|TestRegistration' -v 2>&1 | head -30`
Expected: compile FAILURE (`undefined: geo.PrecisionForRadius`, `undefined: geo.ContainerName`; old encoder registers `"geo"` and still references deleted `segment.RegisterContainer` symbols).

- [ ] **Step 3: Rewrite `container/geo/encoder.go`**

Replace the entire content of `container/geo/encoder.go` with:

```go
// Package geo provides radius-based geo targeting ("within R meters of a
// point") on top of the standard term dictionary — no custom container.
//
// Build: the circle (lat, lng, radius) is expanded into a covering set of
// geohash cells via proximityhash; each cell code is stored as a plain term
// posting in the field's FlatDict / FlatPostingList.
//
// Query: the query point (lat, lng) is geohash-encoded at max precision and
// expanded into all prefixes of length [minPrecision, maxQueryPrecision];
// each prefix is a normal term lookup. A non-empty intersection between the
// query prefixes and any stored covering cell yields a candidate.
//
// Registration: only a PredicateEncoder is registered (container name
// "proximitygeo"). No segment.RegisterContainer call — the segment writers
// skip container-block building for unregistered kinds, so the field is a
// pure dictionary field.
package geo

import (
	"fmt"
	"math"
	"sort"

	"github.com/echoface/proximityhash"
	"github.com/mmcloughlin/geohash"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/util"
)

// ContainerName is the FieldMeta.Container value that selects this encoder.
const ContainerName = "proximitygeo"

const (
	// minPrecision bounds compression: stored covering codes are never
	// shorter than this, and query prefixes start at this length.
	minPrecision = 3
	// maxQueryPrecision is the finest stored/queried geohash length (~38m cell).
	maxQueryPrecision = 8
	// maxRadiusMeters bounds the covering cell count (~128 cells at precision 3).
	maxRadiusMeters = 1_000_000
)

// GeoParam is the build-time predicate value: circle center and radius in meters.
type GeoParam struct {
	Lat    float64
	Lng    float64
	Radius int // meters, (0, maxRadiusMeters]
}

// GeoQuery is the query-time assignment value: a point.
type GeoQuery struct {
	Lat float64
	Lng float64
}

// precisionTable maps geohash precision (code length) to the approximate
// cell width in meters (longitude direction at the equator). Values follow
// proximityhash's grid table.
var precisionTable = []struct {
	precision int
	meters    float64
}{
	{3, 156_500},
	{4, 39_100},
	{5, 4_900},
	{6, 1_200},
	{7, 152},
	{8, 38},
}

// PrecisionForRadius picks the coarsest precision whose cell width is at most
// radius/2, so the over-coverage error (~half cell diagonal) stays a bounded
// fraction of the radius. Falls back to the finest precision for tiny radii.
// Reference points (matching docs/geo-design-v3.md §5.1):
// 50km→5, 5km→6, 1km→7, 100m→8.
func PrecisionForRadius(radiusMeters int) int {
	half := float64(radiusMeters) / 2
	for _, pt := range precisionTable {
		if pt.meters <= half {
			return pt.precision
		}
	}
	return maxQueryPrecision
}

// Encoder implements parser.PredicateEncoder for proximitygeo fields.
type Encoder struct{}

var _ parser.PredicateEncoder = Encoder{}

// Build expands the circle into covering geohash cells and emits one plain
// term posting per cell. Output is deduplicated and sorted so built segments
// stay deterministic (CreateGeohash emits duplicates; CompressGeoHash
// returns map-ordered codes).
func (e Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("proximitygeo: nil value expression")
	}
	param, ok := expr.Value.(GeoParam)
	if !ok {
		return nil, fmt.Errorf("proximitygeo: expected geo.GeoParam, got %T", expr.Value)
	}
	if err := validateLatLng(param.Lat, param.Lng); err != nil {
		return nil, err
	}
	if param.Radius <= 0 || param.Radius > maxRadiusMeters {
		return nil, fmt.Errorf("proximitygeo: radius %d out of range (0, %d]", param.Radius, maxRadiusMeters)
	}

	prec := PrecisionForRadius(param.Radius)
	codes := proximityhash.CreateGeohash(param.Lat, param.Lng, float64(param.Radius), uint(prec))
	codes = proximityhash.CompressGeoHash(codes, minPrecision, prec)
	codes = util.DistinctString(codes)
	sort.Strings(codes)

	out := make([]parser.EncodedPosting, 0, len(codes))
	for _, c := range codes {
		out = append(out, parser.EncodedPosting{Kind: parser.PostingKindTerm, Term: c})
	}
	return out, nil
}

// Query geohash-encodes the point at max precision and emits one term lookup
// per prefix length in [minPrecision, maxQueryPrecision].
func (e Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	q, ok := value.(GeoQuery)
	if !ok {
		return nil, fmt.Errorf("proximitygeo: expected geo.GeoQuery, got %T", value)
	}
	if err := validateLatLng(q.Lat, q.Lng); err != nil {
		return nil, err
	}

	gh := geohash.EncodeWithPrecision(q.Lat, q.Lng, maxQueryPrecision)
	out := make([]parser.EncodedQuery, 0, maxQueryPrecision-minPrecision+1)
	for p := minPrecision; p <= len(gh); p++ {
		out = append(out, parser.EncodedQuery{Kind: parser.QueryKindTerm, Term: gh[:p]})
	}
	return out, nil
}

func validateLatLng(lat, lng float64) error {
	if math.IsNaN(lat) || math.IsNaN(lng) || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return fmt.Errorf("proximitygeo: invalid coordinate (%v, %v)", lat, lng)
	}
	return nil
}

func init() {
	parser.RegisterPredicateEncoder(ContainerName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./container/geo/ -v 2>&1 | tail -20`
Expected: all PASS. If `TestCoveringProperty` fails on an inside-point: verify the failing distance — points sampled at ≤0.8r must be covered; if proximityhash's corner-sampling misses cells at 0.8r for the 300m case, tighten sampling to 0.7r ONCE and leave a comment citing the observed gap (do not loosen further; a miss at ≤0.7r is a real bug — stop and investigate `precisionTable`/compression args).

- [ ] **Step 5: Verify no framework files changed, then commit**

```bash
git diff --stat            # must show ONLY container/geo/* changes
git add container/geo/
git commit -m "feat: geo radius targeting via proximityhash covering terms

Replace the geo custom container with plain term postings per
docs/geo-design-v3.md: build expands circles to covering geohash cells,
query expands points to multi-precision prefixes over FlatDict."
```

---

### Task 2: Geo end-to-end correctness tests (segment round-trip)

**Files:**
- Modify: `container/geo/geo_test.go` (append e2e tests)

**Interfaces:**
- Consumes (root package re-exports, all existing): `be_indexer.BuildSegment(w io.Writer, fields map[BEField]*FieldMeta, docs []*Document) (Entries, error)`, `be_indexer.NewSegmentReader([]byte) (*SegmentReader, error)`, `be_indexer.NewEngine(fields map[core.BEField]*core.FieldMeta, wildcardEntries core.Entries, segments []*segment.SegmentReader) (*BooleanEngine, error)` (= `engine.NewBooleanEngine`, engine/searcher.go:25), `(*BooleanEngine).Retrieve(queries core.Assignments, opts ...core.IndexOpt) (*core.BitmapDocSet, error)` (engine/searcher.go:62), `BitmapDocSet.ForEach(fn func(DocID))` (core/doc_set.go:80), `be_indexer.Assignments`, `be_indexer.NewDocument`, `be_indexer.NewConjunction().Include/Exclude`.
- Consumes from Task 1: `geo.GeoParam`, `geo.GeoQuery`, `geo.ContainerName`.
- Produces: regression suite proving doc §9.3 boundary behaviors.

- [ ] **Step 1: Append the failing-first e2e test**

Append to `container/geo/geo_test.go` (add imports `"bytes"` and `be_indexer "github.com/echoface/be_indexer"` to the import block):

```go
func retrieve(t *testing.T, eng *be_indexer.Engine, assigns be_indexer.Assignments) []be_indexer.DocID {
	t.Helper()
	res, err := eng.Retrieve(assigns)
	if err != nil {
		t.Fatalf("retrieve %+v: %v", assigns, err)
	}
	var ids be_indexer.DocIDList
	res.ForEach(func(id be_indexer.DocID) { ids = append(ids, id) })
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func TestGeoEndToEnd(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"location": {Field: "location", FieldOption: be_indexer.FieldOption{Container: geo.ContainerName}},
		"city":     {Field: "city", FieldOption: be_indexer.FieldOption{Container: "default", Tokenizer: "default"}},
	}

	beijing := geo.GeoParam{Lat: 39.9042, Lng: 116.4074, Radius: 5000}
	shanghai := geo.GeoParam{Lat: 31.2304, Lng: 121.4737, Radius: 5000}

	docs := []*be_indexer.Document{
		// doc1: Beijing 5km circle only
		be_indexer.NewDocument(1).AddConjunction(
			be_indexer.NewConjunction().Include("location", beijing)),
		// doc2: Shanghai 5km circle only
		be_indexer.NewDocument(2).AddConjunction(
			be_indexer.NewConjunction().Include("location", shanghai)),
		// doc3: no geo predicate at all
		be_indexer.NewDocument(3).AddConjunction(
			be_indexer.NewConjunction().Include("city", "gz")),
		// doc4: K=2 — Beijing circle AND city=bj
		be_indexer.NewDocument(4).AddConjunction(
			be_indexer.NewConjunction().Include("location", beijing).Include("city", "bj")),
		// doc5: city=bj AND NOT within Beijing circle
		be_indexer.NewDocument(5).AddConjunction(
			be_indexer.NewConjunction().Include("city", "bj").Exclude("location", beijing)),
		// doc6: same center as doc1, smaller radius 1km — same cells family, independent postings
		be_indexer.NewDocument(6).AddConjunction(
			be_indexer.NewConjunction().Include("location", geo.GeoParam{Lat: 39.9042, Lng: 116.4074, Radius: 1000})),
	}

	buf := new(bytes.Buffer)
	wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := be_indexer.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	convey.Convey("geo e2e retrieval", t, func() {
		convey.Convey("query at Beijing center hits Beijing circles", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"location": geo.GeoQuery{Lat: 39.9042, Lng: 116.4074}})
			// doc1 (5km) and doc6 (1km) match; doc4 needs city too; doc5 excluded.
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 6})
		})

		convey.Convey("query ~1.7km from Beijing center still inside 5km, outside 1km error band matters not for doc1", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"location": geo.GeoQuery{Lat: 39.916, Lng: 116.42}})
			convey.So(got, convey.ShouldContain, be_indexer.DocID(1))
			convey.So(got, convey.ShouldNotContain, be_indexer.DocID(2))
		})

		convey.Convey("query at Shanghai hits only Shanghai", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"location": geo.GeoQuery{Lat: 31.2304, Lng: 121.4737}})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})
		})

		convey.Convey("far away point hits nothing", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"location": geo.GeoQuery{Lat: 0, Lng: 0}})
			convey.So(len(got), convey.ShouldEqual, 0)
		})

		convey.Convey("K=2 conjunction requires both fields", func() {
			got := retrieve(t, eng, be_indexer.Assignments{
				"location": geo.GeoQuery{Lat: 39.9042, Lng: 116.4074},
				"city":     "bj",
			})
			// doc1, doc6 (geo only), doc4 (geo+city). doc5 killed by exclude.
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 4, 6})

			got = retrieve(t, eng, be_indexer.Assignments{
				"location": geo.GeoQuery{Lat: 39.9042, Lng: 116.4074},
				"city":     "sh",
			})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 6})
		})

		convey.Convey("exclude geo: city=bj away from Beijing matches doc5", func() {
			got := retrieve(t, eng, be_indexer.Assignments{
				"location": geo.GeoQuery{Lat: 31.2304, Lng: 121.4737},
				"city":     "bj",
			})
			// doc2 (Shanghai geo), doc5 (city=bj, NOT in Beijing circle — Shanghai point ok)
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2, 5})
		})

		convey.Convey("city only, no location assignment", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"city": "bj"})
			// doc5: exclude untriggered (no location assigned) → matches. doc4 needs geo → no.
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{5})
		})
	})
}
```

Also add `"sort"` to the test imports.

- [ ] **Step 2: Run to verify current behavior**

Run: `go test ./container/geo/ -run TestGeoEndToEnd -v 2>&1 | tail -30`
Expected: PASS (Task 1 already implemented the encoder; this task's tests are regression armor, failing only if Task 1 was wrong).

- [ ] **Step 3: Verify expected-failure sanity (mutate one assertion)**

Temporarily change `ShouldResemble, []be_indexer.DocID{1, 6}` (first case) to `{1}` and re-run; expected: FAIL — proves assertions bite. Revert the mutation.

- [ ] **Step 4: Run the full package suite**

Run: `go test ./container/geo/ -v 2>&1 | tail -15`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git diff --stat            # only container/geo/geo_test.go
git add container/geo/geo_test.go
git commit -m "test: geo e2e boundary/exclude/K=2 coverage for term scheme"
```

---

### Task 3: `container/example` — custom container/encoder extension template

**Files:**
- Create: `container/example/example.go`
- Create: `container/example/example_test.go`

**Interfaces:**
- Consumes: `segment.RegisterContainer(kind, readerFactory, builderFactory)`, `segment.ContainerReader{Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error)}`, `segment.ContainerBuilder{Add(term string, ref PostingRef); Build() ([]byte, error)}`, `segment.PostingRef{Offset uint64; Count uint32}`, `segment.NewPostingListAt(block []byte, ref PostingRef) (*FlatPostingList, error)` (segment/posting_list.go:54), `segment.WriteFlatPostingList(entries []core.EntryID) []byte` (segment/posting_list.go:117), `(*FlatPostingList).NewPostingCursor(term core.Term) core.PostingIterator` (segment/posting_list.go:108), `core.NewTerm(field, term)`, `parser.RegisterPredicateEncoder`, `parser.ValuesToStrings(value interface{}) ([]string, error)`, `util.DistinctString`.
- Produces: `example.ContainerName = "example_prefix"`, `example.Encoder`, `example.NewBuilder() segment.ContainerBuilder`, `example.NewReader([]byte) (segment.ContainerReader, error)` — the copy-paste template for library users' custom containers.

- [ ] **Step 1: Write the failing tests**

Create `container/example/example_test.go`:

```go
package example_test

import (
	"bytes"
	"sort"
	"testing"

	"github.com/smartystreets/goconvey/convey"

	be_indexer "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/container/example"
	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

func TestContainerRoundTrip(t *testing.T) {
	convey.Convey("builder → bytes → reader → retrieve", t, func() {
		eidA := core.NewEntryID(core.NewConjID(101, 0, 1), true)
		eidB := core.NewEntryID(core.NewConjID(102, 0, 1), true)

		plA := segment.WriteFlatPostingList(core.Entries{eidA})
		plB := segment.WriteFlatPostingList(core.Entries{eidB})
		postingBlock := append(append([]byte{}, plA...), plB...)

		cb := example.NewBuilder()
		cb.Add("/api", segment.PostingRef{Offset: 0, Count: 1})
		cb.Add("/static", segment.PostingRef{Offset: uint64(len(plA)), Count: 1})
		blob, err := cb.Build()
		convey.So(err, convey.ShouldBeNil)

		cr, err := example.NewReader(blob)
		convey.So(err, convey.ShouldBeNil)

		convey.Convey("prefix hit returns the matching posting", func() {
			iters, err := cr.Retrieve(postingBlock, "path", "/api/v1/users")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
			convey.So(iters[0].Current(), convey.ShouldEqual, eidA)
		})

		convey.Convey("no prefix match returns nothing", func() {
			iters, err := cr.Retrieve(postingBlock, "path", "/other")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 0)
		})

		convey.Convey("non-string query is an error", func() {
			_, err := cr.Retrieve(postingBlock, "path", 42)
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("truncated blob is an error", func() {
			_, err := example.NewReader(blob[:len(blob)-3])
			convey.So(err, convey.ShouldNotBeNil)
			_, err = example.NewReader([]byte{1})
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func retrieve(t *testing.T, eng *be_indexer.Engine, assigns be_indexer.Assignments) []be_indexer.DocID {
	t.Helper()
	res, err := eng.Retrieve(assigns)
	if err != nil {
		t.Fatalf("retrieve %+v: %v", assigns, err)
	}
	var ids be_indexer.DocIDList
	res.ForEach(func(id be_indexer.DocID) { ids = append(ids, id) })
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// TestExtensionEndToEnd guards the full custom-container chain for library
// users: RegisterPredicateEncoder + RegisterContainer → doc_exporter default
// branch → segment container block → SegmentReader load → engine
// default branch → ContainerQuery → Retrieve.
func TestExtensionEndToEnd(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"path": {Field: "path", FieldOption: be_indexer.FieldOption{Container: example.ContainerName}},
		"city": {Field: "city", FieldOption: be_indexer.FieldOption{Container: "default", Tokenizer: "default"}},
	}
	docs := []*be_indexer.Document{
		be_indexer.NewDocument(1).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/api")),
		be_indexer.NewDocument(2).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/static")),
		be_indexer.NewDocument(3).AddConjunction(
			be_indexer.NewConjunction().Include("city", "bj")),
		be_indexer.NewDocument(4).AddConjunction(
			be_indexer.NewConjunction().Include("path", "/api").Include("city", "bj")),
	}

	buf := new(bytes.Buffer)
	wildcards, err := be_indexer.BuildSegment(buf, fields, docs)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := be_indexer.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := be_indexer.NewEngine(fields, wildcards, []*be_indexer.SegmentReader{reader})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	convey.Convey("extension e2e", t, func() {
		convey.Convey("container block exists and answers directly", func() {
			iters, err := reader.ContainerQuery("path", example.ContainerName, "/api/v1")
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(iters), convey.ShouldEqual, 1)
		})

		convey.Convey("engine default branch routes custom kind", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"path": "/api/v1/users"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1})

			got = retrieve(t, eng, be_indexer.Assignments{"path": "/staticfiles/x.css"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{2})

			got = retrieve(t, eng, be_indexer.Assignments{"path": "/nomatch"})
			convey.So(len(got), convey.ShouldEqual, 0)
		})

		convey.Convey("custom container composes with normal fields (K=2)", func() {
			got := retrieve(t, eng, be_indexer.Assignments{"path": "/api/x", "city": "bj"})
			convey.So(got, convey.ShouldResemble, []be_indexer.DocID{1, 3, 4})
		})
	})
}
```

Note: `core.PostingIterator` (= `core.TermIterator`, `core/definitions.go:111`) exposes `Current() EntryID` — the assertion above uses it. `segment.WriteFlatPostingList(entries []core.EntryID) []byte` is at `segment/posting_list.go:117`; `NewPostingListAt` at `:54`; `(*FlatPostingList).NewPostingCursor(term core.Term) core.PostingIterator` at `:108`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./container/example/ 2>&1 | head -10`
Expected: compile FAILURE — package `example` does not exist.

- [ ] **Step 3: Implement `container/example/example.go`**

```go
// Package example is a minimal, end-to-end template showing how library
// users plug a custom index container + predicate encoder into be_indexer
// WITHOUT touching engine/segment/builder code:
//
//  1. Implement parser.PredicateEncoder: translate business predicates into
//     EncodedPosting (build) and EncodedQuery (query) with a custom Kind.
//  2. Implement segment.ContainerBuilder/ContainerReader: serialize the
//     per-field term→PostingRef table into a byte block and answer queries
//     against it with zero-copy posting cursors.
//  3. Register both in init() under the same name; users select it via
//     FieldMeta.FieldOption{Container: ContainerName}.
//
// Data flow:
//
//	Build:  doc_exporter (default Kind branch) → sink.AddPosting(term)
//	        → segment writer → ContainerBuilder.Add(term, ref) → block bytes
//	Query:  engine initCursors (default Kind branch)
//	        → SegmentReader.ContainerQuery(field, Kind, Value)
//	        → ContainerReader.Retrieve → PostingIterators → K-Groups merge
//
// The demo semantic: stored terms are path prefixes; a query string matches
// every stored term that prefixes it (e.g. stored "/api" matches query
// "/api/v1/users"). Copy this package as a starting point for real
// containers (interval trees, tries, bloom-gated dictionaries, ...).
package example

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
	"github.com/echoface/be_indexer/util"
)

// ContainerName selects this container/encoder pair in FieldMeta.Container.
const ContainerName = "example_prefix"

// --- encoder (build/query translation boundary) ---

// Encoder implements parser.PredicateEncoder. Build-side values are prefix
// strings; query-side values are full strings tested against those prefixes.
type Encoder struct{}

var _ parser.PredicateEncoder = Encoder{}

// Build emits one posting per distinct prefix pattern. Kind is the custom
// container name: doc_exporter routes non-builtin kinds through the standard
// term→posting path, and the segment writer hands every (term, PostingRef)
// of the field to our ContainerBuilder.
func (Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
	if expr == nil {
		return nil, fmt.Errorf("example: nil value expression")
	}
	if expr.Operator != core.ValueOptEQ {
		return nil, fmt.Errorf("%w: example encoder only supports EQ, got %d", core.ErrUnsupportedPredicate, expr.Operator)
	}
	patterns, err := parser.ValuesToStrings(expr.Value)
	if err != nil {
		return nil, err
	}
	patterns = util.DistinctString(patterns)
	sort.Strings(patterns)
	out := make([]parser.EncodedPosting, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			return nil, fmt.Errorf("example: empty prefix pattern")
		}
		if len(p) > 255 {
			return nil, fmt.Errorf("example: pattern longer than 255 bytes")
		}
		out = append(out, parser.EncodedPosting{Kind: parser.PostingKind(ContainerName), Term: p})
	}
	return out, nil
}

// Query emits one container lookup per assignment string. Kind must equal the
// registered container name: the engine's default branch dispatches it via
// SegmentReader.ContainerQuery(field, Kind, Value).
func (Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	texts, err := parser.ValuesToStrings(value)
	if err != nil {
		return nil, err
	}
	out := make([]parser.EncodedQuery, 0, len(texts))
	for _, s := range texts {
		out = append(out, parser.EncodedQuery{Kind: parser.QueryKind(ContainerName), Value: s})
	}
	return out, nil
}

// --- build side (segment serialization) ---

// Builder accumulates (term, PostingRef) pairs and serializes them.
// Binary layout (all integers little-endian, matching segment conventions):
//
//	[count uint32]
//	repeat count times, sorted by term ascending:
//	  [termLen uint8][term bytes][offset uint64][entryCount uint32]
type Builder struct {
	terms []termRef
}

type termRef struct {
	term string
	ref  segment.PostingRef
}

// NewBuilder creates a fresh builder; the segment writer calls it once per field.
func NewBuilder() segment.ContainerBuilder {
	return &Builder{}
}

func (b *Builder) Add(term string, ref segment.PostingRef) {
	b.terms = append(b.terms, termRef{term: term, ref: ref})
}

func (b *Builder) Build() ([]byte, error) {
	sort.Slice(b.terms, func(i, j int) bool { return b.terms[i].term < b.terms[j].term })

	size := 4
	for _, t := range b.terms {
		if len(t.term) > 255 {
			return nil, fmt.Errorf("example: term %q longer than 255 bytes", t.term)
		}
		size += 1 + len(t.term) + 8 + 4
	}
	buf := make([]byte, 0, size)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(b.terms)))
	for _, t := range b.terms {
		buf = append(buf, uint8(len(t.term)))
		buf = append(buf, t.term...)
		buf = binary.LittleEndian.AppendUint64(buf, t.ref.Offset)
		buf = binary.LittleEndian.AppendUint32(buf, t.ref.Count)
	}
	return buf, nil
}

// --- query side (zero-copy reader) ---

// Reader answers prefix queries over the serialized block. Created once at
// SegmentReader construction; Retrieve must be safe for concurrent use
// (read-only state).
type Reader struct {
	entries []termRef
}

// NewReader decodes the block produced by Builder.Build.
func NewReader(b []byte) (segment.ContainerReader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("example: truncated header")
	}
	count := binary.LittleEndian.Uint32(b[0:4])
	r := &Reader{entries: make([]termRef, 0, count)}
	off := 4
	for i := uint32(0); i < count; i++ {
		if off+1 > len(b) {
			return nil, fmt.Errorf("example: truncated term length at entry %d", i)
		}
		tl := int(b[off])
		off++
		if off+tl+12 > len(b) {
			return nil, fmt.Errorf("example: truncated entry %d", i)
		}
		term := string(b[off : off+tl])
		off += tl
		ref := segment.PostingRef{
			Offset: binary.LittleEndian.Uint64(b[off:]),
			Count:  binary.LittleEndian.Uint32(b[off+8:]),
		}
		off += 12
		r.entries = append(r.entries, termRef{term: term, ref: ref})
	}
	return r, nil
}

// Retrieve returns posting cursors for every stored prefix that prefixes the
// query string. Linear scan keeps the template simple; production containers
// should exploit their layout (binary search, trie, interval tree...).
func (r *Reader) Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	q, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("example: query must be string, got %T", query)
	}
	var iters []core.PostingIterator
	for _, e := range r.entries {
		if !strings.HasPrefix(q, e.term) {
			continue
		}
		pl, err := segment.NewPostingListAt(postingBlock, e.ref)
		if err != nil {
			return nil, fmt.Errorf("example: term %q posting: %w", e.term, err)
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, e.term)))
	}
	return iters, nil
}

func init() {
	parser.RegisterPredicateEncoder(ContainerName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
	segment.RegisterContainer(ContainerName,
		func(b []byte) (segment.ContainerReader, error) { return NewReader(b) },
		func() segment.ContainerBuilder { return NewBuilder() },
	)
}
```

All referenced symbols verified against the codebase: `core.ValueOptEQ`, `core.ErrUnsupportedPredicate` (parser/encoder.go:253 uses both), `pl.NewPostingCursor` shape confirmed at segment/posting_list.go:108.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./container/example/ -v 2>&1 | tail -20`
Expected: all PASS.

- [ ] **Step 5: Verify scope, then commit**

```bash
git diff --stat            # only container/example/*
git add container/example/
git commit -m "feat: container/example — custom container/encoder extension template

Out-of-package e2e guard for the pluggable index chain:
RegisterPredicateEncoder/RegisterContainer → exporter default branch →
segment container block → engine default branch → ContainerQuery."
```

---

### Task 4: Honest AddMeta/Value comments + full verification

**Files:**
- Modify: `builder/doc_exporter.go:196-201` (comment only)
- Modify: `segment/container.go:22-38` (comments only)
- Modify: `AGENTS.md` (Code Structure tree: add `container/`)

**Interfaces:**
- Consumes: nothing new. Comment-only + docs; zero behavior change (verify: `go build ./...` output identical, tests untouched).
- Produces: accurate docs for future container authors.

- [ ] **Step 1: Fix the doc_exporter default-branch comment**

In `builder/doc_exporter.go`, replace:

```go
					default:
						// Custom container: standard term→posting path. Container-specific
						// metadata (posting.Value) is stored alongside for the builder.
```

with:

```go
					default:
						// Custom container: the term still flows through the standard
						// term→posting path; the segment writer later feeds every
						// (term, PostingRef) of the field to the registered
						// ContainerBuilder. NOTE: posting.Value is NOT propagated —
						// ContainerMetaBuilder/AddMeta is reserved and currently
						// unwired (would require carrying metadata through the
						// posting sink and external sort).
```

- [ ] **Step 2: Fix the ContainerMetaBuilder comments in `segment/container.go`**

Replace:

```go
// ContainerBuilder accumulates terms and their PostingRefs during segment
// construction and compiles them into a serialized byte block. Build is called
// once per field; after Build the builder is discarded.
//
// If the builder also implements ContainerMetaBuilder (an optional interface),
// the segment writer will call AddMeta when the encoder produced a Value in
// its EncodedPosting, allowing custom containers to receive per-term metadata.
```

with:

```go
// ContainerBuilder accumulates terms and their PostingRefs during segment
// construction and compiles them into a serialized byte block. Build is called
// once per field; after Build the builder is discarded.
```

and replace:

```go
// ContainerMetaBuilder is an optional interface that ContainerBuilder
// implementations may satisfy. When a PredicateEncoder includes a Value in
// its EncodedPosting, the segment writer passes it through AddMeta so the
// container can capture term-specific build metadata.
```

with:

```go
// ContainerMetaBuilder is a RESERVED optional interface for future per-term
// build metadata (EncodedPosting.Value → AddMeta). The built-in segment
// writers do NOT invoke it yet: wiring requires carrying metadata through the
// posting sink and the external-sort spill format. Do not rely on AddMeta
// being called until that lands.
```

- [ ] **Step 3: Add `container/` to the AGENTS.md Code Structure tree**

In `AGENTS.md`, inside the ` ```text ` Code Structure block, insert after the `engine/` subtree lines:

```text
├── container/              # 包外容器与编码器
│   ├── geo/                # proximitygeo: proximityhash 覆盖 cell → 纯 term 索引（无自定义容器）
│   └── example/            # 自定义 container/encoder 扩展模板（含端到端测试）
```

- [ ] **Step 4: Full verification**

Run: `go vet ./... && make test 2>&1 | tail -20`
Expected: vet silent; all package tests PASS (including untouched engine/segment/builder shadow tests — proves zero behavior change outside `container/`).

Run: `gofmt -l . | grep -v vendor || true`
Expected: no output (all files formatted).

- [ ] **Step 5: Commit**

```bash
git diff --stat            # only builder/doc_exporter.go, segment/container.go, AGENTS.md (comment/docs lines)
git add builder/doc_exporter.go segment/container.go AGENTS.md
git commit -m "docs: honest ContainerMetaBuilder/Value comments; document container/ layout"
```

---

## Self-Review Notes

- Spec coverage: doc §3/§4 flow → Task 1; §5 precision (with approved delta rule) → Task 1; §6/§7 zero interface change → enforced by Global Constraints `git diff --stat` gates; §9.3 boundary table → Task 2 cases; §10 file list (encoder.go rewrite, geo.go delete, test rewrite) → Tasks 1-2; user delta B (example package) → Task 3; user delta C → Task 4.
- Known judgment call encoded in Task 1 Step 4: covering-sample margin 0.8r (tighten to 0.7r max once, with comment) — proximityhash samples cell corners, so exact-boundary cells can be missed; the doc (§9.1) already accepts bounded boundary error.
- Type consistency: `geo.ContainerName`/`example.ContainerName`, `PrecisionForRadius`, `retrieve()` helper duplicated per test package intentionally (test packages cannot share helpers without a new export).
