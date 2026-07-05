package geo

import (
	"fmt"
	"sort"
	"strings"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/parser"
	"github.com/echoface/be_indexer/segment"
)

// GeoParam is the build-time predicate value: a center point and radius in meters.
type GeoParam struct {
	Lat    float64
	Lng    float64
	Radius int // meters
}

// precisionTable maps cumulative geohash lengths to approximate cell dimensions.
// Values are approximate (at the equator) and used only for precision selection.
var precisionTable = []struct {
	precision int
	meters    int
}{
	{1, 5_000_000},
	{2, 1_250_000},
	{3, 156_000},
	{4, 39_000},
	{5, 4_900},
	{6, 1_200},
	{7, 152},
	{8, 38},
	{9, 5},
	{10, 1},
}

// geohashBase32 maps 5-bit integers to geohash base32 characters.
const geohashBase32 = "0123456789bcdefghjkmnpqrstuvwxyz"

// Encoder translates geo predicates into geohash-based postings.
//
// Build: GeoParam{Lat, Lng, Radius} → EncodedPosting{Kind: "geo", Term: geohash, Value: GeoParam}
// Query: GeoPoint{Lat, Lng} → EncodedQuery{Kind: "geo", Value: geohash string}
type Encoder struct{}

// GeoQuery is the query-side input: a latitude/longitude point.
type GeoQuery struct {
	Lat float64
	Lng float64
}

// Build implements PredicateEncoder.Build. It tokenizes a geo predicate into
// a single geohash term at the appropriate precision for the radius.
func (e Encoder) Build(expr *core.ValueExpr) ([]parser.EncodedPosting, error) {
	param, ok := expr.Value.(GeoParam)
	if !ok {
		return nil, fmt.Errorf("geo: expected GeoParam, got %T", expr.Value)
	}

	prec := precisionForRadius(param.Radius)
	gh := encodeGeohash(param.Lat, param.Lng, prec)

	return []parser.EncodedPosting{
		{Kind: "geo", Term: gh, Value: param},
	}, nil
}

// Query implements PredicateEncoder.Query. It encodes a query point as a geohash.
func (e Encoder) Query(value interface{}) ([]parser.EncodedQuery, error) {
	q, ok := value.(GeoQuery)
	if !ok {
		return nil, fmt.Errorf("geo: expected GeoQuery, got %T", value)
	}
	prec := 8 // query always uses high precision (cell ~38m)
	gh := encodeGeohash(q.Lat, q.Lng, prec)

	return []parser.EncodedQuery{
		{Kind: "geo", Value: gh},
	}, nil
}

// precisionForRadius returns the geohash precision that bounds a cell roughly
// 2x the given radius, so that a prefix match covers the query region.
func precisionForRadius(radiusMeters int) int {
	target := radiusMeters * 4
	for _, pt := range precisionTable {
		if pt.meters <= target {
			return pt.precision
		}
	}
	return precisionTable[len(precisionTable)-1].precision
}

// encodeGeohash encodes (lat, lng) to a geohash string of given precision.
// This is a simplified implementation with ~5m accuracy at the equator.
func encodeGeohash(lat, lng float64, precision int) string {
	var out strings.Builder
	out.Grow(precision)

	latLo, latHi := -90.0, 90.0
	lngLo, lngHi := -180.0, 180.0
	bit := 0
	ch := 0

	for out.Len() < precision {
		if bit%2 == 0 {
			mid := (lngLo + lngHi) / 2
			if lng > mid {
				ch = (ch << 1) | 1
				lngLo = mid
			} else {
				ch = (ch << 1) | 0
				lngHi = mid
			}
		} else {
			mid := (latLo + latHi) / 2
			if lat > mid {
				ch = (ch << 1) | 1
				latLo = mid
			} else {
				ch = (ch << 1) | 0
				latHi = mid
			}
		}
		bit++
		if bit%5 == 0 {
			out.WriteByte(geohashBase32[ch])
			ch = 0
		}
	}
	return out.String()
}

func init() {
	parser.RegisterPredicateEncoder("geo", func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
	segment.RegisterContainer("geo",
		func(b []byte) (segment.ContainerReader, error) { return NewReader(b) },
		func() segment.ContainerBuilder { return NewBuilder() },
	)
}

var _ parser.PredicateEncoder = Encoder{}

// Ensure sort is used
var _ = sort.Strings
