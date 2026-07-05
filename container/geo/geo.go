// Package geo implements a geohash-based spatial container for be_indexer.
//
// Build: predicates of the form "within R meters of (lat, lng)" are encoded
// as geohash terms at an appropriate precision. The container builds a prefix
// trie that maps geohash prefixes to posting-list offsets.
//
// Query: a query point (lat, lng) is geohash-encoded and the trie returns all
// posting refs whose geohash prefix matches, enabling radius-in/circle-out
// filtering via the posting-list cursors.
//
// Registration:
//
//	func init() {
//	    segment.RegisterContainer("geo", geo.NewReader, geo.NewBuilder)
//	}
package geo

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/segment"
)

// --- geo-specific types ---

// GeoPoint carries a geohash string and the PostingRef locating its
// entries in the segment posting block.
type GeoPoint struct {
	Geohash string
	Ref     segment.PostingRef
}

// --- build side ---

// Builder compiles a geohash → PostingRef mapping into a compact binary trie.
// The binary layout:
//
//	[nodeCount uint32]
//	for each node:
//	  [prefixLen uint8] [prefix bytes ...] [refCount uint32]
//	  for each ref:
//	    [offset uint64] [count uint32]
type Builder struct {
	// accumulated geohash → PostingRef pairs, one per term
	points []GeoPoint
}

// NewBuilder creates a fresh geo container builder.
func NewBuilder() segment.ContainerBuilder {
	return &Builder{}
}

func (b *Builder) Add(term string, ref segment.PostingRef) {
	b.points = append(b.points, GeoPoint{Geohash: term, Ref: ref})
}

func (b *Builder) Build() ([]byte, error) {
	// Sort by geohash prefix for deterministic output + prefix compression.
	sort.Slice(b.points, func(i, j int) bool {
		return b.points[i].Geohash < b.points[j].Geohash
	})

	// Estimate size. Each record: 1B prefixLen + up to 12B geohash + 4B count + 12B per ref.
	size := 4 // nodeCount
	for _, p := range b.points {
		size += 1 + len(p.Geohash) + 4 + 12
	}
	buf := make([]byte, 0, size)

	// nodeCount
	nc := uint32(len(b.points))
	buf = append(buf, byte(nc), byte(nc>>8), byte(nc>>16), byte(nc>>24))

	for _, p := range b.points {
		plen := uint8(len(p.Geohash))
		buf = append(buf, plen)
		buf = append(buf, []byte(p.Geohash)...)
		// refCount = 1 per node in this simple layout
		buf = binary.LittleEndian.AppendUint32(buf, 1)
		buf = binary.LittleEndian.AppendUint64(buf, p.Ref.Offset)
		buf = binary.LittleEndian.AppendUint32(buf, p.Ref.Count)
	}

	return buf, nil
}

// --- query side ---

// Reader reads the geo trie and answers point-in-radius queries.
type Reader struct {
	b      []byte
	count  uint32
	points []entry // decoded index
}

type entry struct {
	geohash string
	ref     segment.PostingRef
}

// NewReader decodes the geo container block produced by Builder.Build() into
// a read-only query index.
func NewReader(b []byte) (segment.ContainerReader, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("geo: truncated header")
	}
	count := binary.LittleEndian.Uint32(b[0:4])
	r := &Reader{b: b, count: count, points: make([]entry, count)}

	off := 4
	for i := uint32(0); i < count; i++ {
		if off >= len(b) {
			return nil, fmt.Errorf("geo: truncated node at index %d", i)
		}
		plen := uint8(b[off])
		off++
		if off+int(plen) > len(b) {
			return nil, fmt.Errorf("geo: truncated geohash at index %d", i)
		}
		r.points[i].geohash = string(b[off : off+int(plen)])
		off += int(plen)

		if off+4 > len(b) {
			return nil, fmt.Errorf("geo: truncated refCount at index %d", i)
		}
		refCount := binary.LittleEndian.Uint32(b[off:])
		off += 4

		if off+12 > len(b) {
			return nil, fmt.Errorf("geo: truncated ref at index %d", i)
		}
		r.points[i].ref.Offset = binary.LittleEndian.Uint64(b[off:])
		r.points[i].ref.Count = binary.LittleEndian.Uint32(b[off+8:])
		off += 12

		_ = refCount // always 1 in this simple layout
	}

	return r, nil
}

// Retrieve performs a geohash prefix lookup for the query point and returns
// posting cursors for all matching geo predicates.
//
// query must be a string (the geohash of the query point). The container
// returns all registered points whose geohash is a prefix match.
func (r *Reader) Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	q, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("geo: query must be string (geohash), got %T", query)
	}

	var iters []core.PostingIterator
	for _, pt := range r.points {
		if !prefixMatch(q, pt.geohash) {
			continue
		}
		pl, err := segment.NewPostingListAt(postingBlock, pt.ref)
		if err != nil {
			continue
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, q)))
	}
	return iters, nil
}

// prefixMatch checks whether q is a prefix of target or vice versa.
// Both the query geohash and the stored geohash are prefixes that can
// be of different lengths depending on the precision.
func prefixMatch(queryHash, storedHash string) bool {
	n := len(queryHash)
	if len(storedHash) < n {
		n = len(storedHash)
	}
	return queryHash[:n] == storedHash[:n]
}
