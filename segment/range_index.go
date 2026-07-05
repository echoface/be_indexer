package segment

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// RangeIndex is a memory-mappable segment tree that answers stabbing queries:
// given a point q, return all EntryIDs whose indexed closed interval [lo, hi]
// contains q.
//
// It is the physical container behind the "ext_range" field type and powers
// numeric range targeting (>, <, between) with no bucketing / approximation:
// a query point either lies inside an interval or it does not.
//
// Design
//
//	1. Elementary intervals. All distinct interval endpoints are collected and
//	   sorted. They partition the int64 line into "elementary intervals" that
//	   alternate between a single point and the open gap between adjacent points
//	   (plus the two unbounded ends). Every indexed interval is exactly a union
//	   of consecutive elementary intervals (leaves).
//	2. Segment tree. A balanced binary tree is built over the leaves. Each
//	   indexed interval is decomposed into O(log m) canonical nodes whose covered
//	   value range is fully contained in the interval; the EntryID is stored on
//	   those nodes only.
//	3. Stabbing. To answer a point query we descend from the root to the leaf
//	   covering q, unioning the posting list of every node on the path. Because
//	   canonical coverage is disjoint along any root-leaf path, an EntryID is
//	   collected at most once (correctness invariant).
//
// Binary layout (all little-endian; the posting region reuses the 8-byte
// aligned FlatPostingList format):
//
//	[magic       "BEIRNG1\0" (8B)]
//	[nodeCount   uint32]
//	[reserved    uint32 = 0]
//	[nodes       nodeRecord × nodeCount]   // 24B each, see nodeRecord
//	[pad to 8B]
//	[posting region: concatenated 8-byte aligned FlatPostingList blocks]
//
// nodeRecord (24 bytes, fixed):
//
//	[split   int64]   // for internal node: max value routed to the left child;
//	                  // q <= split -> left, else right. Unused for leaves.
//	[left    int32]   // dense child id, -1 if leaf
//	[right   int32]   // dense child id, -1 if leaf
//	[postOff uint32]  // posting offset into posting region + 1; 0 means empty
//	[pad     uint32]
//
// Node 0 is the root; the root covers (-inf, +inf), so any int64 q is in range.
type RangeIndex struct {
	nodeCount uint32
	nodes     []nodeRecord
	postings  []byte
}

const rangeNodeRecordSize = 24

var rangeMagic = []byte("BEIRNG1\x00")

type nodeRecord struct {
	split   int64
	left    int32
	right   int32
	postOff uint32
}

// Interval is a closed integer interval [Lo, Hi] tagged with an EntryID.
type Interval struct {
	Lo    int64
	Hi    int64
	Entry core.EntryID
}

// --- build side ---

type rangeBuilder struct {
	bounds []int64     // ascending lower bounds of elementary intervals (leaves)
	nodes  []buildNode // dense node array, node 0 = root
}

type buildNode struct {
	lo, hi   int64 // value coverage
	left     int32
	right    int32
	split    int64
	postings []core.EntryID
}

// buildElementaryBounds returns the ascending lower bounds of every elementary
// interval derived from distinct sorted endpoints.
//
// For points p0<...<p{m-1} the elementary intervals (in order) are:
//
//	(-inf, p0-1], [p0,p0], [p0+1,p1-1], [p1,p1], ..., [p{m-1},p{m-1}], [p{m-1}+1,+inf)
//
// represented by their inclusive lower bound. bounds[0] = MinInt64.
func buildElementaryBounds(points []int64) []int64 {
	bounds := make([]int64, 0, 2*len(points)+1)
	bounds = append(bounds, math.MinInt64)
	for i, p := range points {
		bounds = append(bounds, p)
		if i+1 < len(points) {
			if p+1 <= points[i+1]-1 {
				bounds = append(bounds, p+1)
			}
		} else if p != math.MaxInt64 {
			bounds = append(bounds, p+1)
		}
	}
	return bounds
}

func leafCovers(bounds []int64, idx int) (int64, int64) {
	lo := bounds[idx]
	if idx+1 < len(bounds) {
		return lo, bounds[idx+1] - 1
	}
	return lo, math.MaxInt64
}

// buildTree recursively constructs a balanced tree over leaves [l, r] and
// returns the dense id of the created node.
func (b *rangeBuilder) buildTree(l, r int) int32 {
	id := int32(len(b.nodes))
	lo, _ := leafCovers(b.bounds, l)
	_, hi := leafCovers(b.bounds, r)
	b.nodes = append(b.nodes, buildNode{lo: lo, hi: hi, left: -1, right: -1})
	if l == r {
		return id
	}
	mid := (l + r) / 2
	_, splitVal := leafCovers(b.bounds, mid) // max value routed left
	left := b.buildTree(l, mid)
	right := b.buildTree(mid+1, r)
	b.nodes[id].left = left
	b.nodes[id].right = right
	b.nodes[id].split = splitVal
	return id
}

// insert applies canonical decomposition of [vlo, vhi] starting at node id.
func (b *rangeBuilder) insert(id int32, vlo, vhi int64, eid core.EntryID) {
	n := &b.nodes[id]
	if vhi < n.lo || n.hi < vlo {
		return
	}
	if vlo <= n.lo && n.hi <= vhi {
		n.postings = append(n.postings, eid)
		return
	}
	// internal node guaranteed (a leaf is always fully covered or disjoint given
	// elementary intervals), but guard anyway.
	if n.left < 0 {
		return
	}
	left, right := n.left, n.right
	b.insert(left, vlo, vhi, eid)
	b.insert(right, vlo, vhi, eid)
}

// BuildRangeIndex serializes the given intervals into a RangeIndex byte block.
func BuildRangeIndex(intervals []Interval) ([]byte, error) {
	if len(intervals) == 0 {
		return encodeRangeIndex(nil, nil), nil
	}

	ptSet := make(map[int64]struct{}, len(intervals)*2)
	for _, iv := range intervals {
		if iv.Lo > iv.Hi {
			return nil, fmt.Errorf("invalid interval [%d,%d]", iv.Lo, iv.Hi)
		}
		ptSet[iv.Lo] = struct{}{}
		ptSet[iv.Hi] = struct{}{}
	}
	points := make([]int64, 0, len(ptSet))
	for p := range ptSet {
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })

	b := &rangeBuilder{bounds: buildElementaryBounds(points)}
	b.buildTree(0, len(b.bounds)-1)

	for _, iv := range intervals {
		b.insert(0, iv.Lo, iv.Hi, iv.Entry)
	}

	// emit posting region + node records
	nodes := make([]nodeRecord, len(b.nodes))
	var postingRegion []byte
	for i := range b.nodes {
		bn := &b.nodes[i]
		rec := nodeRecord{split: bn.split, left: bn.left, right: bn.right}
		if len(bn.postings) > 0 {
			sort.Slice(bn.postings, func(x, y int) bool { return bn.postings[x] < bn.postings[y] })
			if pad := (8 - len(postingRegion)%8) % 8; pad != 0 {
				postingRegion = append(postingRegion, make([]byte, pad)...)
			}
			rec.postOff = uint32(len(postingRegion)) + 1
			postingRegion = append(postingRegion, WriteFlatPostingList(bn.postings)...)
		}
		nodes[i] = rec
	}

	return encodeRangeIndex(nodes, postingRegion), nil
}

func encodeRangeIndex(nodes []nodeRecord, postingRegion []byte) []byte {
	nodeCount := uint32(len(nodes))
	headerSize := 16
	nodesSize := int(nodeCount) * rangeNodeRecordSize
	prefix := headerSize + nodesSize
	if pad := (8 - prefix%8) % 8; pad != 0 {
		prefix += pad
	}

	buf := make([]byte, prefix+len(postingRegion))
	copy(buf[0:8], rangeMagic)
	binary.LittleEndian.PutUint32(buf[8:12], nodeCount)
	off := headerSize
	for _, n := range nodes {
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(n.split))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], uint32(n.left))
		binary.LittleEndian.PutUint32(buf[off+12:off+16], uint32(n.right))
		binary.LittleEndian.PutUint32(buf[off+16:off+20], n.postOff)
		// off+20:off+24 reserved padding
		off += rangeNodeRecordSize
	}
	copy(buf[prefix:], postingRegion)
	return buf
}

// NewRangeIndexReader maps a serialized RangeIndex block.
func NewRangeIndexReader(b []byte) (*RangeIndex, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("truncated range index header")
	}
	if string(b[0:8]) != string(rangeMagic) {
		return nil, fmt.Errorf("invalid range index magic")
	}
	nodeCount := binary.LittleEndian.Uint32(b[8:12])
	if nodeCount == 0 {
		return &RangeIndex{}, nil
	}

	headerSize := 16
	nodesSize := int(nodeCount) * rangeNodeRecordSize
	prefix := headerSize + nodesSize
	if pad := (8 - prefix%8) % 8; pad != 0 {
		prefix += pad
	}
	if len(b) < prefix {
		return nil, fmt.Errorf("truncated range index nodes")
	}

	nodes := make([]nodeRecord, nodeCount)
	off := headerSize
	for i := range nodes {
		nodes[i] = nodeRecord{
			split:   int64(binary.LittleEndian.Uint64(b[off : off+8])),
			left:    int32(binary.LittleEndian.Uint32(b[off+8 : off+12])),
			right:   int32(binary.LittleEndian.Uint32(b[off+12 : off+16])),
			postOff: binary.LittleEndian.Uint32(b[off+16 : off+20]),
		}
		off += rangeNodeRecordSize
	}

	return &RangeIndex{
		nodeCount: nodeCount,
		nodes:     nodes,
		postings:  b[prefix:],
	}, nil
}

// Stab returns posting iterators for every interval that contains q. Iterators
// are zero-copy views into the mapped posting region.
func (ri *RangeIndex) Stab(field core.BEField, q int64) ([]core.PostingIterator, error) {
	if ri.nodeCount == 0 {
		return nil, nil
	}
	var iters []core.PostingIterator
	id := int32(0)
	for id >= 0 {
		n := &ri.nodes[id]
		if off := n.postOff; off != 0 {
			pl, err := NewFlatPostingList(ri.postings[off-1:])
			if err != nil {
				return nil, err
			}
			iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, q)))
		}
		if n.left < 0 { // leaf
			break
		}
		if q <= n.split {
			id = n.left
		} else {
			id = n.right
		}
	}
	return iters, nil
}

// Retrieve implements ContainerReader by performing a stabbing query on the
// segment tree, returning posting cursors into the provided posting block.
func (ri *RangeIndex) Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	point, ok := query.(int64)
	if !ok {
		return nil, fmt.Errorf("range container expects int64 query, got %T", query)
	}
	return ri.Stab(field, point)
}

func init() {
	RegisterContainer(core.IndexNameExtendRange,
		func(b []byte) (ContainerReader, error) { return NewRangeIndexReader(b) },
		nil, // range builder uses BuildRangeIndex directly, not ContainerBuilder
	)
}
