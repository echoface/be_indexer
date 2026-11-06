package storage

import "fmt"

// MemoryReport describes the retained bytes of an immutable Segment.
type MemoryReport struct {
	FieldCount       int     `json:"field_count"`
	TermCount        int     `json:"term_count"`
	DictionaryBytes  int     `json:"dictionary_bytes"`
	PostingBytes     int     `json:"posting_bytes"`
	MetadataBytes    int     `json:"metadata_bytes"`
	TotalBytes       int     `json:"total_bytes"`
	CompressionRatio float64 `json:"compression_ratio"`
}

// MemoryReport returns an approximate in-memory footprint of the Segment.
func (s *Segment) MemoryReport() MemoryReport {
	if s == nil {
		return MemoryReport{}
	}
	metadata := len(s.terms) * termRecordSize
	rawBytes := 0
	for _, record := range s.terms {
		rawBytes += int(record.count) * 8
	}
	total := len(s.strings) + len(s.data) + metadata
	ratio := 0.0
	if rawBytes > 0 {
		ratio = float64(len(s.data)) / float64(rawBytes)
	}
	return MemoryReport{
		FieldCount:       len(s.fields),
		TermCount:        len(s.terms),
		DictionaryBytes:  len(s.strings),
		PostingBytes:     len(s.data),
		MetadataBytes:    metadata,
		TotalBytes:       total,
		CompressionRatio: ratio,
	}
}

// TermInfo contains explainable metadata for one dictionary term.
type TermInfo struct {
	ID       TermID          `json:"id"`
	Field    string          `json:"field"`
	Value    string          `json:"value"`
	Count    uint32          `json:"count"`
	Bytes    uint32          `json:"bytes"`
	Encoding PostingEncoding `json:"encoding"`
}

// InspectTerm returns metadata for a term without decoding its full posting.
func (s *Segment) InspectTerm(field, value string) (TermInfo, error) {
	id, ok := s.LookupTerm(field, value)
	if !ok {
		return TermInfo{}, ErrTermNotFound
	}
	record := s.terms[id]
	return TermInfo{
		ID: id, Field: field, Value: value,
		Count: record.count, Bytes: record.postLen, Encoding: record.codec,
	}, nil
}

// SamplePosting returns at most limit IDs for safe online inspection.
func (s *Segment) SamplePosting(field, value string, limit int) ([]uint64, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must not be negative")
	}
	ids, err := s.PostingFor(field, value)
	if err != nil {
		return nil, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return append([]uint64(nil), ids...), nil
}
