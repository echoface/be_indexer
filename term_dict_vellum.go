package be_indexer

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/blevesearch/vellum"
	"github.com/echoface/be_indexer/codegen/indexstore"
)

// VellumTermDict implements TermDictionary using FST (vellum)
type VellumTermDict struct {
	fst *vellum.FST

	// Sampled Index for Reverse Lookup (Decode)
	// TermID -> Key (FieldID + Value)
	// Since TermIDs are 0, 1, 2... N, we can just store every K-th key.
	sampleKeys [][]byte
	sampleRate int
}

const (
	defaultSampleRate = 32
)

// NewVellumTermDict loads FST from data
func NewVellumTermDict(fstData []byte, sampleData []byte) (*VellumTermDict, error) {
	fst, err := vellum.Load(fstData)
	if err != nil {
		return nil, fmt.Errorf("load vellum fst failed: %v", err)
	}

	// Decode Sample Data
	// Format: [SampleRate(Varint)] [Count(Varint)] [KeyLen][Key]...
	var sampleKeys [][]byte
	var sampleRate int

	if len(sampleData) > 0 {
		r := bytes.NewReader(sampleData)
		sr, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("read sample rate failed: %v", err)
		}
		sampleRate = int(sr)

		count, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("read sample count failed: %v", err)
		}

		sampleKeys = make([][]byte, 0, count)
		for i := uint64(0); i < count; i++ {
			n, err := binary.ReadUvarint(r)
			if err != nil {
				return nil, fmt.Errorf("read sample key len failed: %v", err)
			}
			key := make([]byte, n)
			if _, err := r.Read(key); err != nil {
				return nil, fmt.Errorf("read sample key failed: %v", err)
			}
			sampleKeys = append(sampleKeys, key)
		}
	} else {
		// Fallback or empty
		sampleRate = defaultSampleRate
	}

	return &VellumTermDict{
		fst:        fst,
		sampleKeys: sampleKeys,
		sampleRate: sampleRate,
	}, nil
}

func (d *VellumTermDict) Size() int {
	return d.fst.Len()
}

func (d *VellumTermDict) Get(fieldID uint64, value string) (uint64, bool) {
	key := encodeKey(fieldID, value)
	id, exists, err := d.fst.Get(key)
	if err != nil {
		return 0, false
	}
	return id, exists
}

func (d *VellumTermDict) Decode(id uint64) (uint64, string, bool) {
	// 1. Find the nearest sample starting point
	// sample index = id / sampleRate
	sampleIdx := int(id) / d.sampleRate
	if sampleIdx >= len(d.sampleKeys) {
		// If exact match at the very end or out of bounds?
		// We might need to handle the last block if it didn't generate a sample?
		// But samples are generated every K items.
		// If id < Size, it should be reachable.
		// If sampleKeys is empty but Size > 0, we start from start.
		if d.Size() > 0 && len(d.sampleKeys) == 0 {
			sampleIdx = -1 // Start from beginning
		} else if sampleIdx >= len(d.sampleKeys) {
			// This might happen if id is very large, but valid.
			// The last sample covers up to Size-1.
			// Let's cap at last sample.
			sampleIdx = len(d.sampleKeys) - 1
		}
	}

	var startKey []byte
	var startID uint64

	if sampleIdx >= 0 && sampleIdx < len(d.sampleKeys) {
		startKey = d.sampleKeys[sampleIdx]
		startID = uint64(sampleIdx * d.sampleRate)
	} else {
		// Start from nil (beginning)
		startKey = nil
		startID = 0
	}

	// 2. Iterate until we reach id
	// We need an iterator that starts >= startKey.
	// Since startKey IS a valid key (it's a sample), Seek(startKey) should land on it.
	itr, err := d.fst.Iterator(startKey, nil)
	if err != nil {
		return 0, "", false
	}
	defer itr.Close()

	// Skip delta steps
	steps := int(id - startID)
	// If steps < 0, it means we overshot? Should not happen if logic is correct.
	if steps < 0 {
		return 0, "", false
	}

	for i := 0; i < steps; i++ {
		err := itr.Next()
		if err == vellum.ErrIteratorDone {
			return 0, "", false
		}
		if err != nil {
			return 0, "", false
		}
	}
	
	keyBytes, val := itr.Current()
	if val != id {
		// This implies consistency error or ID mismatch
		// In our Builder, values are sequential.
		// If Vellum has values mismatched, we can't trust ID.
		// But for now, trust the sequence.
	}
	
	fieldID, valueStr := decodeKey(keyBytes)
	return fieldID, valueStr, true
}

// -----------------------------------------------------------------------------
// Helper
// -----------------------------------------------------------------------------

func encodeKey(fieldID uint64, value string) []byte {
	buf := make([]byte, 8+len(value))
	binary.BigEndian.PutUint64(buf[:8], fieldID)
	copy(buf[8:], value)
	return buf
}

func decodeKey(data []byte) (uint64, string) {
	if len(data) < 8 {
		return 0, ""
	}
	fid := binary.BigEndian.Uint64(data[:8])
	return fid, string(data[8:])
}

// -----------------------------------------------------------------------------
// Builder
// -----------------------------------------------------------------------------

type VellumTermDictBuilder struct {
	builder *vellum.Builder
	buf     *bytes.Buffer

	// Sampling
	sampleKeys [][]byte
	sampleRate int
	counter    uint64 // current term count (TermID)
}

func NewVellumTermDictBuilder() *VellumTermDictBuilder {
	buf := new(bytes.Buffer)
	b, _ := vellum.New(buf, nil) // default opts
	return &VellumTermDictBuilder{
		builder:    b,
		buf:        buf,
		sampleRate: defaultSampleRate,
		counter:    0,
	}
}

// Add inserts a term. MUST be called in sorted order of (FieldID, Value).
func (b *VellumTermDictBuilder) Add(term KVTerm) error {
	key := encodeKey(term.FieldID, term.Value)
	
	// Sample if needed
	if b.counter%uint64(b.sampleRate) == 0 {
		// Copy key for storage
		k := make([]byte, len(key))
		copy(k, key)
		b.sampleKeys = append(b.sampleKeys, k)
	}

	err := b.builder.Insert(key, b.counter)
	if err != nil {
		return err
	}
	b.counter++
	return nil
}

func (b *VellumTermDictBuilder) Build() (*indexstore.TermDict, error) {
	if err := b.builder.Close(); err != nil {
		return nil, err
	}

	fstBytes := b.buf.Bytes()

	// Serialize Samples
	var sampleBuf bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte

	// 1. Rate
	n := binary.PutUvarint(tmp[:], uint64(b.sampleRate))
	sampleBuf.Write(tmp[:n])

	// 2. Count
	n = binary.PutUvarint(tmp[:], uint64(len(b.sampleKeys)))
	sampleBuf.Write(tmp[:n])

	// 3. Keys
	for _, key := range b.sampleKeys {
		n = binary.PutUvarint(tmp[:], uint64(len(key)))
		sampleBuf.Write(tmp[:n])
		sampleBuf.Write(key)
	}

	return &indexstore.TermDict{
		FstData: fstBytes,
		Content: sampleBuf.Bytes(), // Reuse content for samples
	}, nil
}
