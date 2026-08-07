package segment

import (
	"encoding/binary"
	"fmt"

	"github.com/echoface/be_indexer/core"
)

// ACBuilder builds an Aho-Corasick automaton serialized as a Double-Array
// Trie (DAT) for zero-copy mmap matching.
//
// Alphabet compression
//
// Transitions are addressed by base[state] + symbolID rather than
// base[state] + rune. Every rune that appears in any pattern is mapped to a
// dense symbol id in [1, symCount] (0 is reserved as "unknown", guaranteeing an
// unknown input rune always fails). This keeps the DAT address space proportional
// to the actual alphabet size instead of the full Unicode range (up to
// 0x10FFFF), which is what makes Chinese / emoji / arbitrary-codepoint patterns
// practical: array size and findBase cost scale with distinct characters used,
// not with the maximum codepoint value.
type ACBuilder struct {
	nodes   []flatNode
	outputs [][]uint32
	refs    []PostingRef

	symOf map[rune]uint32
	syms  []rune

	collector *KeyedPostingCollector
}

type flatNode struct {
	char       rune
	firstChild uint32
	sibling    uint32
	fail       uint32
}

func NewACBuilder(env BuilderEnv) *ACBuilder {
	builder := &ACBuilder{
		nodes:   make([]flatNode, 1, 1024),
		outputs: make([][]uint32, 1, 1024),
		symOf:   make(map[rune]uint32),
		syms:    []rune{0},
		collector: NewKeyedPostingCollector(env.MaxPostingsInMemory, env.TmpDir),
	}
	return builder
}

// internSymbol returns the dense symbol id for ch, allocating one on first use.
func (b *ACBuilder) internSymbol(ch rune) uint32 {
	if id, ok := b.symOf[ch]; ok {
		return id
	}
	id := uint32(len(b.syms))
	b.syms = append(b.syms, ch)
	b.symOf[ch] = id
	return id
}

// AddRecord accumulates term → entry mappings via the collector.
func (b *ACBuilder) AddRecord(record any, entries []core.EntryID) error {
	term, ok := record.(string)
	if !ok {
		termBytes, ok2 := record.([]byte)
		if !ok2 {
			return fmt.Errorf("ACBuilder: expected string or []byte record, got %T", record)
		}
		return b.collector.Add(termBytes, entries)
	}
	return b.collector.Add([]byte(term), entries)
}

func (b *ACBuilder) AddPosting(term string, ref PostingRef) error {
	termID := uint32(len(b.refs))
	b.refs = append(b.refs, ref)

	runes := []rune(term)
	if len(runes) == 0 {
		return nil
	}

	curr := uint32(0)
	for _, ch := range runes {
		b.internSymbol(ch)
		curr = b.addChild(curr, ch)
	}

	b.outputs[curr] = append(b.outputs[curr], termID)
	return nil
}

func (b *ACBuilder) addChild(parentIdx uint32, ch rune) uint32 {
	parent := &b.nodes[parentIdx]

	if parent.firstChild == 0 {
		newIdx := uint32(len(b.nodes))
		b.nodes = append(b.nodes, flatNode{char: ch})
		b.outputs = append(b.outputs, nil)

		b.nodes[parentIdx].firstChild = newIdx
		return newIdx
	}

	curr := parent.firstChild
	var prev uint32 = 0

	for curr != 0 {
		node := &b.nodes[curr]
		if node.char == ch {
			return curr
		}
		if node.char > ch {
			newIdx := uint32(len(b.nodes))
			b.nodes = append(b.nodes, flatNode{char: ch, sibling: curr})
			b.outputs = append(b.outputs, nil)

			if prev == 0 {
				b.nodes[parentIdx].firstChild = newIdx
			} else {
				b.nodes[prev].sibling = newIdx
			}
			return newIdx
		}
		prev = curr
		curr = node.sibling
	}

	newIdx := uint32(len(b.nodes))
	b.nodes = append(b.nodes, flatNode{char: ch})
	b.outputs = append(b.outputs, nil)
	b.nodes[prev].sibling = newIdx
	return newIdx
}

func (b *ACBuilder) buildFailPointers() {
	queue := make([]uint32, 0, 1024)

	child := b.nodes[0].firstChild
	for child != 0 {
		b.nodes[child].fail = 0
		queue = append(queue, child)
		child = b.nodes[child].sibling
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		child := b.nodes[curr].firstChild
		for child != 0 {
			ch := b.nodes[child].char
			queue = append(queue, child)

			failState := b.nodes[curr].fail

			for {
				transition := b.findChild(failState, ch)
				if transition != 0 {
					b.nodes[child].fail = transition
					break
				}
				if failState == 0 {
					b.nodes[child].fail = 0
					break
				}
				failState = b.nodes[failState].fail
			}

			failNode := b.nodes[child].fail
			if len(b.outputs[failNode]) > 0 {
				b.outputs[child] = append(b.outputs[child], b.outputs[failNode]...)
			}

			child = b.nodes[child].sibling
		}
	}
}

func (b *ACBuilder) findChild(state uint32, ch rune) uint32 {
	child := b.nodes[state].firstChild
	for child != 0 {
		if b.nodes[child].char == ch {
			return child
		}
		if b.nodes[child].char > ch {
			break
		}
		child = b.nodes[child].sibling
	}
	return 0
}

// buildDAT lays out the trie into base/check arrays addressed by symbol id.
//
// Because symbols are dense (1..symCount), the array stays compact regardless of
// the underlying Unicode codepoints, so the classic nextCheckPos-anchored linear
// probe in findBase stays cheap (probe distance is bounded by the small dense
// alphabet, not by raw rune values).
func (b *ACBuilder) buildDAT() (base, check, stateMap, revMap []uint32, used []bool) {
	base = make([]uint32, 1, len(b.nodes)*2)
	check = make([]uint32, 1, len(b.nodes)*2)
	used = make([]bool, 1, len(b.nodes)*2)
	stateMap = make([]uint32, len(b.nodes))
	revMap = make([]uint32, 1, len(b.nodes)*2)

	base[0] = 1
	check[0] = 0
	used[0] = true
	stateMap[0] = 0
	revMap[0] = 0

	queue := []uint32{0}
	var nextCheckPos uint32 = 1

	growTo := func(n uint32) {
		for uint32(len(base)) < n {
			base = append(base, 0)
			check = append(check, 0)
			used = append(used, false)
			revMap = append(revMap, 0)
		}
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		var childrenSyms []uint32
		var childrenIdx []uint32

		child := b.nodes[curr].firstChild
		for child != 0 {
			childrenSyms = append(childrenSyms, b.symOf[b.nodes[child].char])
			childrenIdx = append(childrenIdx, child)
			queue = append(queue, child)
			child = b.nodes[child].sibling
		}

		if len(childrenSyms) == 0 {
			continue
		}

		baseVal := findBase(used, nextCheckPos, childrenSyms)
		if baseVal > nextCheckPos {
			nextCheckPos = baseVal
		}

		datCurr := stateMap[curr]
		maxSym := uint32(0)
		for _, s := range childrenSyms {
			if s > maxSym {
				maxSym = s
			}
		}
		growTo(baseVal + maxSym + 1)
		base[datCurr] = baseVal

		for i, sym := range childrenSyms {
			datNext := baseVal + sym
			check[datNext] = datCurr
			used[datNext] = true
			stateMap[childrenIdx[i]] = datNext
			revMap[datNext] = childrenIdx[i]
		}
	}

	return base, check, stateMap, revMap, used
}

// findBase returns a base offset such that base+sym is free for every child
// symbol. Probing is anchored on the smallest symbol so base stays >= 1.
func findBase(used []bool, startPos uint32, syms []uint32) uint32 {
	minSym := syms[0]
	for _, s := range syms {
		if s < minSym {
			minSym = s
		}
	}
	base := startPos
	if base <= minSym {
		base = minSym + 1
	}
	base -= minSym
	if base == 0 {
		base = 1
	}
	for {
		valid := true
		for _, s := range syms {
			idx := base + s
			if idx < uint32(len(used)) && used[idx] {
				valid = false
				break
			}
		}
		if valid {
			return base
		}
		base++
	}
}

func (b *ACBuilder) Compile() ([]byte, error) {
	b.buildFailPointers()

	base, check, stateMap, revMap, used := b.buildDAT()
	stateCount := uint32(len(base))

	fail := make([]uint32, stateCount)
	for i := uint32(0); i < stateCount; i++ {
		if used[i] {
			flatNodeIdx := revMap[i]
			if flatNodeIdx < uint32(len(b.nodes)) {
				oldFail := b.nodes[flatNodeIdx].fail
				fail[i] = stateMap[oldFail]
			}
		}
	}

	outputPtrs := make([]uint32, stateCount)
	var outputData []byte

	for i := uint32(0); i < stateCount; i++ {
		if used[i] {
			flatNodeIdx := revMap[i]
			if flatNodeIdx < uint32(len(b.nodes)) {
				outTerms := b.outputs[flatNodeIdx]
				if len(outTerms) > 0 {
					outputPtrs[i] = uint32(len(outputData)) + 1
					// Payload per output node: [count u16] then count *
					// [postingOffset u64][postingCount u32]. Storing the posting
					// ref directly lets the reader build a cursor without a second
					// dictionary lookup or string allocation on the query path.
					var chunk [2]byte
					binary.LittleEndian.PutUint16(chunk[:], uint16(len(outTerms)))
					outputData = append(outputData, chunk[:]...)
					var rec [12]byte
					for _, termID := range outTerms {
						ref := b.refs[termID]
						binary.LittleEndian.PutUint64(rec[0:8], ref.Offset)
						binary.LittleEndian.PutUint32(rec[8:12], ref.Count)
						outputData = append(outputData, rec[:]...)
					}
				}
			}
		}
	}

	// Symbol table: sorted runes assigned dense ids 1..symCount in the same
	// order Add() encountered them. We persist (rune) per dense id; the reader
	// rebuilds rune->id. Persisting in dense-id order keeps decode trivial.
	symCount := uint32(len(b.syms) - 1) // exclude reserved 0

	headerSize := 12 // stateCount(4) + symCount(4) + reserved(4)
	symSize := int(symCount) * 4
	arraySize := int(stateCount) * 4
	totalSize := headerSize + symSize + arraySize*4 + len(outputData)

	buf := make([]byte, totalSize)
	binary.LittleEndian.PutUint32(buf[0:4], stateCount)
	binary.LittleEndian.PutUint32(buf[4:8], symCount)
	binary.LittleEndian.PutUint32(buf[8:12], 0)

	off := headerSize
	for id := uint32(1); id <= symCount; id++ {
		binary.LittleEndian.PutUint32(buf[off:off+4], uint32(b.syms[id]))
		off += 4
	}

	arrBase := off
	for i := 0; i < int(stateCount); i++ {
		binary.LittleEndian.PutUint32(buf[arrBase+i*4:], base[i])
		binary.LittleEndian.PutUint32(buf[arrBase+arraySize+i*4:], check[i])
		binary.LittleEndian.PutUint32(buf[arrBase+arraySize*2+i*4:], fail[i])
		binary.LittleEndian.PutUint32(buf[arrBase+arraySize*3+i*4:], outputPtrs[i])
	}

	copy(buf[arrBase+arraySize*4:], outputData)

	return buf, nil
}

// Build writes the Postings block, then builds the AC automaton and writes it
// as a container block. Each term's PostingRef is folded into the automaton in
// the same single pass that streams its posting list out (via BuildPostings).
func (b *ACBuilder) Build(bw BlockWriter) error {
	n, err := BuildPostings(b.collector, bw, func(key []byte, ref PostingRef) error {
		return b.AddPosting(string(key), ref)
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	acData, err := b.Compile()
	if err != nil {
		return err
	}
	return bw.WriteBlock(core.IndexNameACMatcher, acData)
}
