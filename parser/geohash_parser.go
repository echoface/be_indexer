package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/echoface/be_indexer/util"
	"github.com/echoface/proximityhash"
	"github.com/mmcloughlin/geohash"
)

// GeoHashParser turn value into a unique id
// parser a string fmt geohash region: "lat:lon:radius(m)"
// geohash长度	误差距离（km）
//
//	1	            ±2500
//	2	            ±630
//	3	            ±78
//	4	            ±20
//	5	            ±2.4
//	6	            ±0.61
//	7	            ±0.076
//	8	            ±0.019
type (
	GeoHashParser struct {
		GeoOption
	}
	GeoOption struct {
		Precision               int
		CompressPrecisionMin    int
		CompressPrecisionCutoff int
	}
)

var DefaultGeoHashOption = GeoOption{
	Precision:               6,
	CompressPrecisionMin:    3,
	CompressPrecisionCutoff: 6,
}

func NewGeoHashParser(option *GeoOption) *GeoHashParser {
	if option == nil {
		option = &DefaultGeoHashOption
	}
	p := &GeoHashParser{
		GeoOption: *option,
	}
	p.GeoOption.InitDefault()
	return p
}

func (p *GeoOption) InitDefault() {
	if p.Precision == 0 {
		p.Precision = 6
	}
	if p.CompressPrecisionMin == 0 {
		p.CompressPrecisionMin = 4
	}
	if p.CompressPrecisionCutoff == 0 {
		p.CompressPrecisionCutoff = 6
	}
	p.CompressPrecisionCutoff = util.MinInt(p.CompressPrecisionCutoff, p.Precision)
}

func (p *GeoHashParser) Name() string {
	return "geohash"
}

// lat:lon:radius
func parseLatLonRadius(s string) (lat, lon, r float64, err error) {
	ss := strings.Split(s, ":")
	if len(ss) != 3 {
		return 0, 0, 0, fmt.Errorf("fmt error, need lat:lon:radius")
	}
	if lat, err = strconv.ParseFloat(ss[0], 64); err != nil {
		return
	}
	if lon, err = strconv.ParseFloat(ss[1], 64); err != nil {
		return
	}
	if r, err = strconv.ParseFloat(ss[2], 64); err != nil {
		return
	}
	return
}

func (p *GeoHashParser) genGeoHash(v string) ([]string, error) {
	lat, lon, r, err := parseLatLonRadius(v)
	if err != nil {
		return nil, err
	}
	codes := proximityhash.CreateGeohash(lat, lon, r, uint(p.Precision))
	codes = proximityhash.CompressGeoHash(codes, p.CompressPrecisionMin, p.CompressPrecisionCutoff)
	return codes, nil
}

func (p *GeoHashParser) genQueryAssignGeoHash(lat, lon float64) []string {
	geohashCode := geohash.Encode(lat, lon)
	results := make([]string, 0, p.Precision)
	for i := p.CompressPrecisionMin; i <= p.Precision; i++ {
		geohashID := geohashCode[:i]
		results = append(results, geohashID)
	}
	return results
}

// TokenizeAssign implements ValueTokenizer for query phase
// Parses query coordinates like [30.5, 98.2] into geohash strings
func (p *GeoHashParser) TokenizeAssign(v interface{}) ([]string, error) {
	var lat, lon float64
	switch value := v.(type) {
	case [2]float64:
		lat, lon = value[0], value[1]
	case []float64:
		if len(value) != 2 {
			return nil, fmt.Errorf("need lat/lon value")
		}
		lat, lon = value[0], value[1]
	default:
		return nil, fmt.Errorf("bad query assign fmt, need [lat, lon]")
	}
	return p.genQueryAssignGeoHash(lat, lon), nil
}

// ParseAssign implements ValueIDGenerator for query phase
// Parses query coordinates like [30.5, 98.2] into geohash ids
func (p *GeoHashParser) ParseAssign(v interface{}) ([]uint64, error) {
	codes, err := p.TokenizeAssign(v)
	if err != nil {
		return nil, err
	}
	results := make([]uint64, len(codes))
	for i, code := range codes {
		id, _ := geohash.ConvertStringToInt(code)
		results[i] = id
	}
	return results, nil
}

// TokenizeValue implements ValueTokenizer for indexing phase
// Parses range strings like "30:90:1000" into multiple geohash strings
func (p *GeoHashParser) TokenizeValue(v interface{}) ([]string, error) {
	switch value := v.(type) {
	case string:
		return p.genGeoHash(value)
	case []string:
		results := make([]string, 0)
		for _, v := range value {
			parts, err := p.genGeoHash(v)
			if err != nil {
				return nil, err
			}
			results = append(results, parts...)
		}
		return util.DistinctString(results), nil
	case []interface{}:
		results := make([]string, 0)
		for _, vi := range value {
			s, ok := vi.(string)
			if !ok {
				return nil, fmt.Errorf("need format like lat:lon:radius")
			}
			parts, err := p.genGeoHash(s)
			if err != nil {
				return nil, err
			}
			results = append(results, parts...)
		}
		return util.DistinctString(results), nil
	default:
	}
	return nil, fmt.Errorf("unsupported geohash type")
}

// ParseValue implements ValueIDGenerator for indexing phase
// Parses range strings like "30:90:1000" into multiple geohash ids
func (p *GeoHashParser) ParseValue(v interface{}) ([]uint64, error) {
	codes, err := p.TokenizeValue(v)
	if err != nil {
		return nil, err
	}
	results := make([]uint64, len(codes))
	for i, code := range codes {
		id, _ := geohash.ConvertStringToInt(code)
		results[i] = id
	}
	return results, nil
}
