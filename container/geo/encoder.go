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

// EncoderName is the predicate encoder name registered under this package.
const EncoderName = "proximitygeo"

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
// Callers must validate the radius first (Build enforces (0, maxRadiusMeters]);
// out-of-range input falls back to the finest precision without error.
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
		out = append(out, parser.EncodedPosting{Record: c})
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
		out = append(out, parser.EncodedQuery{Kind: parser.QueryKindTerm, Value: gh[:p]})
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
	parser.RegisterPredicateEncoder(EncoderName, func(core.FieldMeta) (parser.PredicateEncoder, error) {
		return Encoder{}, nil
	})
}
