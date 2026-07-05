package segment

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/echoface/be_indexer/core"
)

type ACMmapReader struct {
	b          []byte
	stateCount uint32
	base       []uint32
	check      []uint32
	fail       []uint32
	outputPtrs []uint32 // state -> offset in outputData
	outputData []byte
	symOf      map[rune]uint32 // rune -> dense symbol id (matches builder)
}

func NewACMmapReader(b []byte) (*ACMmapReader, error) {
	if len(b) < 12 {
		return nil, fmt.Errorf("truncated AC mmap block")
	}

	stateCount := binary.LittleEndian.Uint32(b[0:4])
	symCount := binary.LittleEndian.Uint32(b[4:8])
	if stateCount == 0 {
		return &ACMmapReader{}, nil
	}

	headerSize := 12
	symSize := int(symCount) * 4
	arrayBytes := int(stateCount) * 4
	expectedMin := headerSize + symSize + arrayBytes*4
	if len(b) < expectedMin {
		return nil, fmt.Errorf("truncated AC arrays")
	}

	// Decode symbol table: dense id (1-based) -> rune.
	symOf := make(map[rune]uint32, symCount)
	off := headerSize
	for id := uint32(1); id <= symCount; id++ {
		r := rune(int32(binary.LittleEndian.Uint32(b[off : off+4])))
		symOf[r] = id
		off += 4
	}

	arrBase := off
	basePtr := unsafe.Pointer(&b[arrBase])
	checkPtr := unsafe.Pointer(&b[arrBase+arrayBytes])
	failPtr := unsafe.Pointer(&b[arrBase+arrayBytes*2])
	outputPtrsPtr := unsafe.Pointer(&b[arrBase+arrayBytes*3])

	return &ACMmapReader{
		b:          b,
		stateCount: stateCount,
		base:       unsafe.Slice((*uint32)(basePtr), stateCount),
		check:      unsafe.Slice((*uint32)(checkPtr), stateCount),
		fail:       unsafe.Slice((*uint32)(failPtr), stateCount),
		outputPtrs: unsafe.Slice((*uint32)(outputPtrsPtr), stateCount),
		outputData: b[arrBase+arrayBytes*4:],
		symOf:      symOf,
	}, nil
}

// transition returns the next state from `state` on symbol `sym` (a dense id),
// or 0 if there is no such transition. The bound check uses a 64-bit
// intermediate so base+sym can never overflow uint32 and forge a spurious
// in-range index.
func (ac *ACMmapReader) transition(state, sym uint32) uint32 {
	if sym == 0 {
		return 0
	}
	next := uint64(ac.base[state]) + uint64(sym)
	if next >= uint64(ac.stateCount) {
		return 0
	}
	ns := uint32(next)
	if ns > 0 && ac.check[ns] == state {
		return ns
	}
	return 0
}

// MatchPostingRefs matches patterns directly over a string, iterating runes via
// range (decoding UTF-8 in place, no []rune allocation) and returning the
// posting-list refs of every matched pattern. Each ref points straight into the
// posting block, so the caller builds a cursor without a second dictionary
// lookup or term string allocation.
func (ac *ACMmapReader) MatchPostingRefs(text string) []PostingRef {
	if ac.stateCount == 0 {
		return nil
	}

	var results []PostingRef
	state := uint32(0)
	for _, r := range text {
		state, results = ac.step(state, r, results)
	}
	return results
}

// step advances the automaton by one rune from `state`, appending any matched
// posting refs at the resulting state, and returns the new state and result
// slice.
func (ac *ACMmapReader) step(state uint32, r rune, results []PostingRef) (uint32, []PostingRef) {
	sym := ac.symOf[r] // 0 if the rune never appears in any pattern
	for {
		nextState := ac.transition(state, sym)
		if nextState != 0 {
			state = nextState
			break
		}
		if state == 0 {
			break
		}
		state = ac.fail[state]
	}

	if outOffset := ac.outputPtrs[state]; outOffset != 0 {
		results = ac.readOutputs(outOffset-1, results)
	}
	return state, results
}

// readOutputs decodes the posting refs stored at the output payload `offset`
// and appends them to dst. Payload layout: [count u16] then count *
// [postingOffset u64][postingCount u32].
func (ac *ACMmapReader) readOutputs(offset uint32, dst []PostingRef) []PostingRef {
	if int(offset)+2 > len(ac.outputData) {
		return dst
	}
	count := binary.LittleEndian.Uint16(ac.outputData[offset : offset+2])
	offset += 2

	for i := 0; i < int(count); i++ {
		if int(offset)+12 > len(ac.outputData) {
			break
		}
		off := binary.LittleEndian.Uint64(ac.outputData[offset : offset+8])
		cnt := binary.LittleEndian.Uint32(ac.outputData[offset+8 : offset+12])
		dst = append(dst, PostingRef{Offset: off, Count: cnt})
		offset += 12
	}
	return dst
}

// Retrieve implements ContainerReader by performing AC matching against the
// query text and returning posting cursors into the posting block.
func (ac *ACMmapReader) Retrieve(postingBlock []byte, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	text, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("ac container expects string query, got %T", query)
	}
	refs := ac.MatchPostingRefs(text)
	if len(refs) == 0 {
		return nil, nil
	}
	iters := make([]core.PostingIterator, 0, len(refs))
	for _, ref := range refs {
		pl, err := newPostingListAt(postingBlock, ref)
		if err != nil {
			continue
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, text)))
	}
	return iters, nil
}

func init() {
	RegisterContainer(core.IndexNameACMatcher,
		func(b []byte) (ContainerReader, error) { return NewACMmapReader(b) },
		func() ContainerBuilder { return NewStaticACBuilder() },
	)
}
