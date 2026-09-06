package segment

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/echoface/be_indexer/core"
)

// RangeIndex is the memory-mappable container behind the "ext_range" field
// type. It answers stabbing queries: given a point q, return all EntryIDs whose
// indexed closed interval [lo, hi] contains q. It powers numeric targeting (=,
// in, >, <, between) with no bucketing / approximation.
//
// Hybrid layout: point index + segment tree
//
// A numeric field is dominated by equality predicates (age = v, age in
// (v1..vk)); range predicates (>, <, between) are the minority. Equality
// compiles to a degenerate interval [v, v]. Feeding those single points into a
// segment tree is wasteful: the tree's size is governed by the number of
// distinct endpoints, so every equality value inflates the node count even
// though a point needs no interval decomposition at all.
//
// RangeIndex therefore splits incoming intervals by shape at build time:
//
//   - Point intervals (lo == hi), produced by = / in: stored in a packed,
//     ascending int64 key array with a parallel posting-offset array. A query
//     resolves a point via an allocation-free int64 binary search
//     (cache-friendly, no string term, no offset indirection).
//   - Range intervals (lo < hi), produced by >, <, between and unbounded edges:
//     stored in a balanced segment tree. Only these endpoints enter the tree,
//     so the tree's size tracks the number of true ranges — not the field's
//     value cardinality.
//
// A query point q is answered by unioning both sub-indexes: the point index
// contributes the equality entries at exactly q, and the tree contributes every
// range covering q. The two entry sets are disjoint by construction (a given
// EntryID's interval is either a point or a range), so no double counting is
// possible.
//
// Segment tree design
//
//  1. Elementary intervals. All distinct range endpoints are collected and
//     sorted. They partition the int64 line into "elementary intervals" that
//     alternate between a single point and the open gap between adjacent points
//     (plus the two unbounded ends). Every indexed range is exactly a union of
//     consecutive elementary intervals (leaves).
//  2. Segment tree. A balanced binary tree is built over the leaves. Each range
//     is decomposed into O(log m) canonical nodes whose covered value range is
//     fully contained in the interval; the EntryID is stored on those nodes.
//  3. Stabbing. To answer a point query we descend from the root to the leaf
//     covering q, unioning the posting list of every node on the path. Because
//     canonical coverage is disjoint along any root-leaf path, an EntryID is
//     collected at most once.
//
// Binary layout (all little-endian; the posting region reuses the 8-byte
// aligned FlatPostingList format):
//
//	[magic       "BEIRNG2\0" (8B)]
//	[nodeCount   uint32]            // segment tree nodes (range intervals)
//	[pointCount  uint32]            // distinct point keys (equality intervals)
//	[nodes       nodeRecord × nodeCount]   // 24B each, see nodeRecord
//	[pointKeys   int64      × pointCount]  // ascending
//	[pointOff    uint32     × pointCount]  // posting offset+1 into posting region
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

	// Point index: pointKeys[i] maps to pointOff[i] (posting offset + 1 into the
	// posting region). Keys are ascending so lookups binary-search in place with
	// no allocation.
	pointKeys []int64
	pointOff  []uint32

	postings []byte
}

const rangeNodeRecordSize = 24

var rangeMagic = []byte("BEIRNG2\x00")

type nodeRecord struct {
	split   int64
	left    int32
	right   int32
	postOff uint32
}

// Interval is a closed integer interval [Lo, Hi] tagged with an EntryID.
// Lo == Hi denotes an equality (point) interval; Lo < Hi a true range.
type Interval struct {
	Lo    int64
	Hi    int64
	Entry core.EntryID
}

// --- build side ---

type rangeBuilder struct {
	bounds []int64     // ascending lower bounds of elementary intervals (leaves)
	nodes  []buildNode // dense node array, node 0 = root
	// pairs accumulates every (canonical node, EntryID) assignment in a single
	// flat slice instead of one small []EntryID per node. This collapses the
	// former O(nodeCount) tiny-slice allocations (and their append growth churn)
	// into one contiguous buffer, cutting build-time peak memory and GC load.
	pairs []nodePair
}

// nodePair records that eid is stored on the canonical node id. Sorting the
// flat pair slice by (node, eid) groups every node's postings contiguously and
// in ascending EntryID order, which is exactly what emission needs.
type nodePair struct {
	node int32
	eid  core.EntryID
}

type buildNode struct {
	lo, hi int64 // value coverage
	left   int32
	right  int32
	split  int64
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
		b.pairs = append(b.pairs, nodePair{node: id, eid: eid})
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

// postingEmitter appends 8-byte-aligned FlatPostingList blocks to a single
// posting region and hands back each block's offset+1 (the 0 sentinel means
// "no posting").
type postingEmitter struct {
	region []byte
}

func (pe *postingEmitter) emit(entries []core.EntryID) (uint32, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	if pad := (8 - len(pe.region)%8) % 8; pad != 0 {
		pe.region = append(pe.region, make([]byte, pad)...)
	}
	// The stored value is offset+1 (0 is the "no posting" sentinel), so the
	// region length itself must stay below the uint32 ceiling.
	off, err := checkedU32(len(pe.region), "range posting region offset")
	if err != nil {
		return 0, err
	}
	pl, err := WriteFlatPostingList(entries)
	if err != nil {
		return 0, err
	}
	pe.region = append(pe.region, pl...)
	return off + 1, nil
}

// BuildRangeIndex serializes the given intervals into a RangeIndex byte block.
// Point intervals (Lo == Hi) are routed to the packed point index; range
// intervals (Lo < Hi) are routed to the segment tree.
func BuildRangeIndex(intervals []Interval) ([]byte, error) {
	if len(intervals) == 0 {
		return encodeRangeIndex(nil, nil, nil, nil), nil
	}

	// Partition by shape and collect the tree's endpoint set from ranges only.
	// pointEntries groups equality EntryIDs by value; the segment tree never
	// sees these, so its endpoint set — and thus node count — is bounded by the
	// number of true ranges, not the field's value cardinality.
	pointEntries := make(map[int64][]core.EntryID)
	var ranges []Interval
	ptSet := make(map[int64]struct{})
	for _, iv := range intervals {
		if iv.Lo > iv.Hi {
			return nil, fmt.Errorf("invalid interval [%d,%d]", iv.Lo, iv.Hi)
		}
		if iv.Lo == iv.Hi {
			pointEntries[iv.Lo] = append(pointEntries[iv.Lo], iv.Entry)
			continue
		}
		ranges = append(ranges, iv)
		ptSet[iv.Lo] = struct{}{}
		ptSet[iv.Hi] = struct{}{}
	}

	emitter := &postingEmitter{}

	// --- point index ---
	pointKeys := make([]int64, 0, len(pointEntries))
	for k := range pointEntries {
		pointKeys = append(pointKeys, k)
	}
	sort.Slice(pointKeys, func(i, j int) bool { return pointKeys[i] < pointKeys[j] })
	pointOff := make([]uint32, len(pointKeys))
	for i, k := range pointKeys {
		eids := pointEntries[k]
		sort.Slice(eids, func(x, y int) bool { return eids[x] < eids[y] }) // iterator contract: ascending EntryID
		off, err := emitter.emit(eids)
		if err != nil {
			return nil, err
		}
		pointOff[i] = off
	}

	// --- segment tree over range endpoints only ---
	var nodes []nodeRecord
	if len(ranges) > 0 {
		points := make([]int64, 0, len(ptSet))
		for p := range ptSet {
			points = append(points, p)
		}
		sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })

		b := &rangeBuilder{bounds: buildElementaryBounds(points)}
		b.buildTree(0, len(b.bounds)-1)
		for _, iv := range ranges {
			b.insert(0, iv.Lo, iv.Hi, iv.Entry)
		}

		// Sort the flat (node, eid) pairs so that every node's postings become a
		// contiguous run in ascending EntryID order. This satisfies the
		// PostingIterator ascending-EntryID contract without any per-node sort,
		// and lets us emit each node's FlatPostingList in a single pass.
		sort.Slice(b.pairs, func(x, y int) bool {
			if b.pairs[x].node != b.pairs[y].node {
				return b.pairs[x].node < b.pairs[y].node
			}
			return b.pairs[x].eid < b.pairs[y].eid
		})

		nodes = make([]nodeRecord, len(b.nodes))
		for i := range b.nodes {
			bn := &b.nodes[i]
			nodes[i] = nodeRecord{split: bn.split, left: bn.left, right: bn.right}
		}
		for i := 0; i < len(b.pairs); {
			node := b.pairs[i].node
			j := i
			for j < len(b.pairs) && b.pairs[j].node == node {
				j++
			}
			// pairs[i:j] are this node's EntryIDs, already ascending.
			run := make([]core.EntryID, j-i)
			for k := i; k < j; k++ {
				run[k-i] = b.pairs[k].eid
			}
			off, err := emitter.emit(run)
			if err != nil {
				return nil, err
			}
			nodes[node].postOff = off
			i = j
		}
	}

	return encodeRangeIndex(nodes, pointKeys, pointOff, emitter.region), nil
}

func encodeRangeIndex(nodes []nodeRecord, pointKeys []int64, pointOff []uint32, postingRegion []byte) []byte {
	nodeCount := uint32(len(nodes))
	pointCount := uint32(len(pointKeys))

	headerSize := 16
	nodesSize := int(nodeCount) * rangeNodeRecordSize
	pointsSize := int(pointCount)*8 + int(pointCount)*4 // keys (int64) + offsets (uint32)
	prefix := headerSize + nodesSize + pointsSize
	if pad := (8 - prefix%8) % 8; pad != 0 {
		prefix += pad
	}

	buf := make([]byte, prefix+len(postingRegion))
	copy(buf[0:8], rangeMagic)
	binary.LittleEndian.PutUint32(buf[8:12], nodeCount)
	binary.LittleEndian.PutUint32(buf[12:16], pointCount)

	off := headerSize
	for _, n := range nodes {
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(n.split))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], uint32(n.left))
		binary.LittleEndian.PutUint32(buf[off+12:off+16], uint32(n.right))
		binary.LittleEndian.PutUint32(buf[off+16:off+20], n.postOff)
		// off+20:off+24 reserved padding
		off += rangeNodeRecordSize
	}
	for _, k := range pointKeys {
		binary.LittleEndian.PutUint64(buf[off:off+8], uint64(k))
		off += 8
	}
	for _, o := range pointOff {
		binary.LittleEndian.PutUint32(buf[off:off+4], o)
		off += 4
	}
	copy(buf[prefix:], postingRegion)
	return buf
}

// NewRangeReader maps a serialized RangeIndex block.
func NewRangeReader(b []byte) (*RangeIndex, error) {
	if len(b) < 16 {
		return nil, fmt.Errorf("truncated range index header")
	}
	if string(b[0:8]) != string(rangeMagic) {
		return nil, fmt.Errorf("invalid range index magic")
	}
	nodeCount := binary.LittleEndian.Uint32(b[8:12])
	pointCount := binary.LittleEndian.Uint32(b[12:16])
	if nodeCount == 0 && pointCount == 0 {
		return &RangeIndex{}, nil
	}

	headerSize := 16
	nodesSize := int(nodeCount) * rangeNodeRecordSize
	pointsSize := int(pointCount)*8 + int(pointCount)*4
	prefix := headerSize + nodesSize + pointsSize
	if pad := (8 - prefix%8) % 8; pad != 0 {
		prefix += pad
	}
	if len(b) < prefix {
		return nil, fmt.Errorf("truncated range index body")
	}

	off := headerSize
	var nodes []nodeRecord
	if nodeCount > 0 {
		nodes = make([]nodeRecord, nodeCount)
		for i := range nodes {
			nodes[i] = nodeRecord{
				split:   int64(binary.LittleEndian.Uint64(b[off : off+8])),
				left:    int32(binary.LittleEndian.Uint32(b[off+8 : off+12])),
				right:   int32(binary.LittleEndian.Uint32(b[off+12 : off+16])),
				postOff: binary.LittleEndian.Uint32(b[off+16 : off+20]),
			}
			off += rangeNodeRecordSize
		}
	}

	var pointKeys []int64
	var pointOff []uint32
	if pointCount > 0 {
		pointKeys = make([]int64, pointCount)
		for i := range pointKeys {
			pointKeys[i] = int64(binary.LittleEndian.Uint64(b[off : off+8]))
			off += 8
		}
		pointOff = make([]uint32, pointCount)
		for i := range pointOff {
			pointOff[i] = binary.LittleEndian.Uint32(b[off : off+4])
			off += 4
		}
	}

	return &RangeIndex{
		nodeCount: nodeCount,
		nodes:     nodes,
		pointKeys: pointKeys,
		pointOff:  pointOff,
		postings:  b[prefix:],
	}, nil
}

// findPoint returns the posting offset+1 for exact key q, or 0 if q is not a
// point key. Hand-rolled int64 binary search: no allocation, no interface
// dispatch, cache-friendly over the packed key array.
func (ri *RangeIndex) findPoint(q int64) uint32 {
	lo, hi := 0, len(ri.pointKeys)-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		k := ri.pointKeys[mid]
		if k == q {
			return ri.pointOff[mid]
		}
		if k < q {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return 0
}

// Stab returns posting iterators for every interval that contains q, unioning
// the point index (equality hits at exactly q) and the segment tree (ranges
// covering q). Iterators are zero-copy views into the mapped posting region.
func (ri *RangeIndex) Stab(field core.BEField, q int64) ([]core.PostingIterator, error) {
	var iters []core.PostingIterator

	// Point index: exact equality entries at q.
	if off := ri.findPoint(q); off != 0 {
		pl, err := NewFlatPostingList(ri.postings[off-1:])
		if err != nil {
			return nil, err
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, q)))
	}

	// Segment tree: every range covering q along the root-leaf path.
	if ri.nodeCount > 0 {
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
	}

	return iters, nil
}

// MatchQuery implements IndexReader by performing a stabbing query on the hybrid
// container, returning posting cursors.
func (ri *RangeIndex) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	point, ok := query.(int64)
	if !ok {
		return nil, fmt.Errorf("range container expects int64 query, got %T", query)
	}
	return ri.Stab(field, point)
}

func init() {
	RegisterIndex(core.IndexNameExtendRange, IndexDef{
		Reader: func(b []byte) (IndexReader, error) {
			return NewRangeReader(b)
		},
		Builder: func(env BuilderEnv) IndexBuilder { return NewRangeBuilder(env) },
	})
}
