package geo_test

import (
	"bytes"
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/mmcloughlin/geohash"
	"github.com/smartystreets/goconvey/convey"

	be_indexer "github.com/echoface/be_indexer"
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
		codes = append(codes, ep.Record.(string))
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
	if math.Abs(math.Cos(lat*math.Pi/180)) < 1e-10 {
		return lat + dLat, lng
	}
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
			convey.So(q.Value, convey.ShouldEqual, full[:3+i])
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
			FieldOption: core.FieldOption{Encoder: geo.EncoderName},
		})
		convey.So(err, convey.ShouldBeNil)
		convey.So(enc, convey.ShouldNotBeNil)
		convey.So(segment.HasIndex(geo.EncoderName), convey.ShouldBeFalse)
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

func TestGeoEndToEnd(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"location": {Field: "location", FieldOption: be_indexer.FieldOption{Encoder: geo.EncoderName}},
		"city":     {Field: "city", FieldOption: be_indexer.FieldOption{IndexType: "default"}},
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
			convey.So(got, convey.ShouldNotContain, be_indexer.DocID(6))
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
