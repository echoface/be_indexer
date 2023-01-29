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
type GeoHashParser struct {
	Precision           uint
	MinCompressionLevel uint
}

func (p *GeoHashParser) init() {
	if p.Precision == 0 {
		p.Precision = 6
	}
	if p.MinCompressionLevel == 0 {
		p.MinCompressionLevel = 4
	}
}

func (p *GeoHashParser) Name() string {
	return "geohash"
}

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

func (p *GeoHashParser) genGeoHashID(v string) ([]uint64, error) {
	lat, lon, r, err := parseLatLonRadius(v)
	if err != nil {
		return nil, err
	}
	codes := proximityhash.CreateGeohash(lat, lon, r, p.Precision)
	codes = proximityhash.CompressGeoHash(codes, int(p.MinCompressionLevel))

	results := make([]uint64, len(codes))
	for idx, code := range codes {
		id, _ := geohash.ConvertStringToInt(code)
		results[idx] = id
	}
	return results, nil
}

func (p *GeoHashParser) genQueryAssignGeoHashIDs(lat, lon float64) []uint64 {
	geohashCode := geohash.Encode(lat, lon)
	results := make([]uint64, 0, p.Precision)
	for i := p.MinCompressionLevel; i < p.Precision; i++ {
		geohashID, _ := geohash.ConvertStringToInt(geohashCode[:i])
		results = append(results, geohashID)
	}
	return results
}

// ParseAssign parse query assign value into id-encoded ids
func (p *GeoHashParser) ParseAssign(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case [2]float64:
		return p.genQueryAssignGeoHashIDs(value[0], value[1]), nil
	case []float64:
		if len(value) != 2 {
			return nil, fmt.Errorf("need lat/lon value")
		}
		return p.genQueryAssignGeoHashIDs(value[0], value[1]), nil
	default:
		break
	}
	return nil, fmt.Errorf("bad query assign fmt")
}

// ParseValue parse bool expression value into id-encoded ids
func (p *GeoHashParser) ParseValue(v interface{}) ([]uint64, error) {
	switch value := v.(type) {
	case string:
		return p.genGeoHashID(value)
	case []string:
		results := make([]uint64, 0)
		for _, v := range value {
			parts, err := p.genGeoHashID(v)
			if err != nil {
				return nil, err
			}
			results = append(results, parts...)
		}
		return util.DistinctInteger(results), nil
	case []interface{}:
		results := make([]uint64, 0)
		for _, vi := range value {
			s, ok := vi.(string)
			if !ok {
				return nil, fmt.Errorf("need format like lat:lon:radius")
			}

			parts, err := p.genGeoHashID(s)
			if err != nil {
				return nil, err
			}
			results = append(results, parts...)
		}
		return util.DistinctInteger(results), nil
	default:
		break
	}
	return nil, fmt.Errorf("unsupported geohash type")
}
