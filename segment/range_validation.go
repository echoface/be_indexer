package segment

import (
	"encoding/binary"
	"fmt"
	"math"
)

// validate establishes every structural invariant needed by Stab, once during
// load. It does not scan EntryIDs. The iterative walk also rejects cycles and
// shared/unreachable children without risking a stack overflow on bad input.
func (ri *RangeIndex) validate() error {
	checkPosting := func(off uint32) error {
		if off == 0 {
			return nil
		}
		start := uint64(off - 1)
		if start%8 != 0 || start > uint64(len(ri.postings)) || uint64(len(ri.postings))-start < postingHeaderSize {
			return fmt.Errorf("range posting offset out of range or unaligned: %d", start)
		}
		count := binary.LittleEndian.Uint32(ri.postings[start : start+4])
		return validatePostingRef(ri.postings, PostingRef{Offset: start, Count: count})
	}
	for i, key := range ri.pointKeys {
		if i > 0 && key <= ri.pointKeys[i-1] {
			return fmt.Errorf("range point keys must be strictly increasing")
		}
		if ri.pointOff[i] == 0 {
			return fmt.Errorf("range point %d has no posting", i)
		}
		if err := checkPosting(ri.pointOff[i]); err != nil {
			return fmt.Errorf("range point %d: %w", i, err)
		}
	}
	if ri.nodeCount == 0 {
		return nil
	}
	type visit struct {
		id     int32
		lo, hi int64
	}
	stack := []visit{{id: 0, lo: math.MinInt64, hi: math.MaxInt64}}
	seen := make([]bool, len(ri.nodes))
	visited := 0
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if v.id < 0 || uint64(v.id) >= uint64(len(ri.nodes)) {
			return fmt.Errorf("range child out of range: %d", v.id)
		}
		if seen[v.id] {
			return fmt.Errorf("range cycle or shared child: %d", v.id)
		}
		seen[v.id] = true
		visited++
		n := ri.nodes[v.id]
		if err := checkPosting(n.postOff); err != nil {
			return fmt.Errorf("range node %d: %w", v.id, err)
		}
		if n.left == -1 && n.right == -1 {
			continue
		}
		if n.left < 0 || n.right < 0 || n.split < v.lo || n.split >= v.hi {
			return fmt.Errorf("invalid range branch at node %d", v.id)
		}
		stack = append(stack, visit{id: n.right, lo: n.split + 1, hi: v.hi}, visit{id: n.left, lo: v.lo, hi: n.split})
	}
	if visited != len(ri.nodes) {
		return fmt.Errorf("range index has unreachable nodes")
	}
	return nil
}
