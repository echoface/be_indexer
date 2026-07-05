package geo_test

import (
	"bytes"
	"testing"

	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/container/geo"
	_ "github.com/echoface/be_indexer/container/geo" // trigger registration
)

func TestGeoRoundTrip(t *testing.T) {
	fields := map[be_indexer.BEField]*be_indexer.FieldMeta{
		"location": {Field: "location", FieldOption: be_indexer.FieldOption{Container: "geo"}},
		"city":     {Field: "city", FieldOption: be_indexer.FieldOption{Container: "default", Tokenizer: "default"}},
	}

	// Beijing: 39.9, 116.4
	// Shanghai: 31.2, 121.5
	bjRadius := be_indexer.NewDocument(1).AddConjunction(
		be_indexer.NewConjunction().Include("location", geo.GeoParam{Lat: 39.9, Lng: 116.4, Radius: 5000}),
	)
	shRadius := be_indexer.NewDocument(2).AddConjunction(
		be_indexer.NewConjunction().Include("location", geo.GeoParam{Lat: 31.2, Lng: 121.5, Radius: 5000}),
	)
	noGeo := be_indexer.NewDocument(3).AddConjunction(
		be_indexer.NewConjunction().Include("city", "gz"),
	)

	buf := new(bytes.Buffer)
	_, err := be_indexer.BuildSegment(buf, fields, []*be_indexer.Document{bjRadius, shRadius, noGeo})
	if err != nil {
		t.Fatal(err)
	}

	reader, err := be_indexer.NewSegmentReader(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := be_indexer.NewEngine(fields, nil, []*be_indexer.SegmentReader{reader})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// Query at exact Beijing center — same geohash cell
	assigns := be_indexer.Assignments{
		"location": geo.GeoQuery{Lat: 39.9, Lng: 116.4},
	}
	results, err := eng.Retrieve(assigns)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != 1 {
		t.Fatalf("expected doc 1 for Beijing query, got %v", results)
	}

	// Query at exact Shanghai center
	assigns = be_indexer.Assignments{
		"location": geo.GeoQuery{Lat: 31.2, Lng: 121.5},
	}
	results, err = eng.Retrieve(assigns)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != 2 {
		t.Fatalf("expected doc 2 for Shanghai query, got %v", results)
	}

	// Query far from both
	assigns = be_indexer.Assignments{
		"location": geo.GeoQuery{Lat: 0, Lng: 0},
	}
	results, err = eng.Retrieve(assigns)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results for remote query, got %v", results)
	}
}

// geoParam is the build-time value. Must match what the geo Encoder expects.
type geoParam struct {
	Lat    float64
	Lng    float64
	Radius int
}

// geoQuery is the query-time value. Must match what the geo Encoder expects.
type geoQuery struct {
	Lat float64
	Lng float64
}
