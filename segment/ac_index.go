package segment

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
	"unsafe"

	"github.com/echoface/be_indexer/core"
)

type ACIndex struct {
	b          []byte
	stateCount uint32
	base       []uint32
	check      []uint32
	fail       []uint32
	outputPtrs []uint32 // state -> offset in outputData
	outputData []byte
	symOf      map[rune]uint32 // rune -> dense symbol id (matches builder)
}

func NewACReader(b []byte) (*ACIndex, error) {
	const headerSize = 12 // stateCount, symCount, reserved
	if len(b) < headerSize {
		return nil, fmt.Errorf("truncated AC mmap block")
	}

	stateCount := binary.LittleEndian.Uint32(b[0:4])
	symCount := binary.LittleEndian.Uint32(b[4:8])
	if reserved := binary.LittleEndian.Uint32(b[8:12]); reserved != 0 {
		return nil, fmt.Errorf("invalid AC reserved header: %d", reserved)
	}
	if stateCount == 0 {
		return &ACIndex{}, nil
	}

	// Use uint64 intermediates: on 32-bit platforms int(symCount)*4 can
	// overflow, wrap negative, and bypass this bound check.
	symBytes := uint64(symCount) * 4
	arrayBytes := uint64(stateCount) * 4
	expectedMin := headerSize + symBytes + arrayBytes*4
	if uint64(len(b)) < expectedMin {
		return nil, fmt.Errorf("truncated AC arrays")
	}

	// Decode symbol table: dense id (1-based) -> rune.
	symOf := make(map[rune]uint32, symCount)
	off := headerSize
	for id := uint32(1); id <= symCount; id++ {
		r := rune(int32(binary.LittleEndian.Uint32(b[off : off+4])))
		if !utf8.ValidRune(r) {
			return nil, fmt.Errorf("invalid AC symbol rune: %#U", r)
		}
		if _, dup := symOf[r]; dup {
			return nil, fmt.Errorf("duplicate AC symbol rune: %#U", r)
		}
		symOf[r] = id
		off += 4
	}

	arrBase := off
	arrBytes := int(arrayBytes) // safe: expectedMin above proved it fits in int
	base := mapUint32s(b[arrBase:arrBase+arrBytes], stateCount)
	check := mapUint32s(b[arrBase+arrBytes:arrBase+arrBytes*2], stateCount)
	fail := mapUint32s(b[arrBase+arrBytes*2:arrBase+arrBytes*3], stateCount)
	outputPtrs := mapUint32s(b[arrBase+arrBytes*3:arrBase+arrBytes*4], stateCount)
	outputData := b[arrBase+arrBytes*4:]

	ac := &ACIndex{
		b:          b,
		stateCount: stateCount,
		base:       base,
		check:      check,
		fail:       fail,
		outputPtrs: outputPtrs,
		outputData: outputData,
		symOf:      symOf,
	}
	if err := ac.validateInvariants(); err != nil {
		return nil, err
	}
	return ac, nil
}

// mapUint32s returns a []uint32 view over raw. It uses a zero-copy unsafe
// mapping when the backing memory is 4-byte aligned and falls back to a decoded
// copy otherwise, so callers never construct a misaligned uint32 slice.
func mapUint32s(raw []byte, count uint32) []uint32 {
	ptr := unsafe.Pointer(&raw[0])
	if uintptr(ptr)&3 == 0 {
		return unsafe.Slice((*uint32)(ptr), count)
	}
	vals := make([]uint32, count)
	for i := uint32(0); i < count; i++ {
		vals[i] = binary.LittleEndian.Uint32(raw[int(i)*4 : int(i)*4+4])
	}
	return vals
}

// validateInvariants checks every array reference the query path will ever
// touch. The automaton is memory-mapped data, so a corrupted block must fail
// segment open once here instead of panicking (or spinning forever on a fail
// cycle) inside Retrieve.
func (ac *ACIndex) validateInvariants() error {
	if ac.fail[0] != 0 {
		return fmt.Errorf("invalid AC root fail link: %d", ac.fail[0])
	}
	for i := uint32(1); i < ac.stateCount; i++ {
		if ac.fail[i] >= ac.stateCount {
			return fmt.Errorf("AC fail link out of range: state=%d fail=%d", i, ac.fail[i])
		}
	}
	if err := ac.validateFailTrees(); err != nil {
		return err
	}

	for i := uint32(0); i < ac.stateCount; i++ {
		ptr := ac.outputPtrs[i]
		if ptr == 0 {
			continue
		}
		off := uint64(ptr - 1)
		if off+2 > uint64(len(ac.outputData)) {
			return fmt.Errorf("AC output offset out of range: state=%d offset=%d data=%d", i, ptr-1, len(ac.outputData))
		}
		count := int(binary.LittleEndian.Uint16(ac.outputData[off : off+2]))
		if off+2+uint64(count)*12 > uint64(len(ac.outputData)) {
			return fmt.Errorf("truncated AC output payload: state=%d offset=%d count=%d", i, ptr-1, count)
		}
	}
	return nil
}

// validateFailTrees proves every fail chain reaches the root. Marks: 0 unseen,
// 1 on the current walk, 2 verified. Meeting a 1 means a cycle; ending at 0 or
// a verified state proves the chain terminates.
func (ac *ACIndex) validateFailTrees() error {
	marks := make([]byte, ac.stateCount)
	var path []uint32
	for i := uint32(1); i < ac.stateCount; i++ {
		if marks[i] != 0 {
			continue
		}
		path = path[:0]
		for s := i; s != 0; s = ac.fail[s] {
			if marks[s] == 1 {
				return fmt.Errorf("AC fail cycle at state=%d", s)
			}
			if marks[s] == 2 {
				break
			}
			marks[s] = 1
			path = append(path, s)
		}
		for _, s := range path {
			marks[s] = 2
		}
	}
	return nil
}

// transition returns the next state from `state` on symbol `sym` (a dense id),
// or 0 if there is no such transition. The bound check uses a 64-bit
// intermediate so base+sym can never overflow uint32 and forge a spurious
// in-range index.
func (ac *ACIndex) transition(state, sym uint32) uint32 {
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
func (ac *ACIndex) MatchPostingRefs(text string) []PostingRef {
	if ac.stateCount == 0 {
		return nil
	}
	return ac.MatchPostingRefsInto(text, nil)
}

// MatchPostingRefsInto is the append-style variant of MatchPostingRefs. Callers
// that match repeatedly (e.g. IndexReader.MatchQuery on the retrieval path) can
// reuse dst across calls to avoid one allocation per query; the returned slice
// aliases dst's storage.
func (ac *ACIndex) MatchPostingRefsInto(text string, dst []PostingRef) []PostingRef {
	if ac.stateCount == 0 {
		return dst
	}

	results := dst[:0]
	state := uint32(0)
	for _, r := range text {
		state, results = ac.step(state, r, results)
	}
	return results
}

// step advances the automaton by one rune from `state`, appending any matched
// posting refs at the resulting state, and returns the new state and result
// slice.
func (ac *ACIndex) step(state uint32, r rune, results []PostingRef) (uint32, []PostingRef) {
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
func (ac *ACIndex) readOutputs(offset uint32, dst []PostingRef) []PostingRef {
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

// MatchQuery implements IndexReader by performing AC matching against the
// query text and returning posting cursors into the posting block.
func (ac *ACIndex) MatchQuery(ctx BlockContext, field core.BEField, query interface{}) ([]core.PostingIterator, error) {
	text, ok := query.(string)
	if !ok {
		return nil, fmt.Errorf("ac container expects string query, got %T", query)
	}
	refs := ac.MatchPostingRefsInto(text, make([]PostingRef, 0, 16))
	if len(refs) == 0 {
		return nil, nil
	}
	iters := make([]core.PostingIterator, 0, len(refs))
	for _, ref := range refs {
		pl, err := NewPostingListAt(ctx.Pl, ref)
		if err != nil {
			return nil, fmt.Errorf("field %s: %w", field, err)
		}
		iters = append(iters, pl.NewPostingCursor(core.NewTerm(field, text)))
	}
	return iters, nil
}

func init() {
	RegisterIndex(core.IndexNameACMatcher, IndexDef{
		Reader:  func(b []byte) (IndexReader, error) { return NewACReader(b) },
		Builder: func(env BuilderEnv) IndexBuilder { return NewACBuilder(env) },
	})
}
