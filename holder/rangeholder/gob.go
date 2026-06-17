package rangeholder

import (
	"bytes"
	"container/list"
	"encoding/gob"

	. "github.com/hankji/be_indexer"
)

func init() {
	gob.Register(&ExtendLgtHolder{})
	gob.Register(&RangeIdx{})
	gob.Register(&RangeEntries{})
	gob.Register(&Range{})
}

func (h *ExtendLgtHolder) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		RangeHolderOption
		Debug     bool
		MaxLen    int               // max length of Entries
		AvgLen    int               // avg length of Entries
		RangeIdx  *RangeIdx         // range expression container
		PlEntries map[int64]Entries // in/not in value expression container
	}{
		RangeHolderOption: h.RangeHolderOption,
		Debug:             h.debug,
		MaxLen:            h.maxLen,
		AvgLen:            h.avgLen,
		RangeIdx:          h.rangeIdx,
		PlEntries:         h.plEntries,
	})
	return buf.Bytes(), err
}

func (h *ExtendLgtHolder) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	tmp := struct {
		RangeHolderOption
		Debug     bool
		MaxLen    int               // max length of Entries
		AvgLen    int               // avg length of Entries
		RangeIdx  *RangeIdx         // range expression container
		PlEntries map[int64]Entries // in/not in value expression container
	}{}
	err := dec.Decode(&tmp)
	if err != nil {
		return err
	}
	h.RangeHolderOption = tmp.RangeHolderOption
	h.debug = tmp.Debug
	h.maxLen = tmp.MaxLen
	h.avgLen = tmp.AvgLen
	h.rangeIdx = tmp.RangeIdx
	h.plEntries = tmp.PlEntries
	return nil
}

func (l *RangeIdx) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	tmp := struct {
		Compiled  bool
		MaxLen    int // max length of Entries
		AvgLen    int // avg length of Entries
		ValueMin  int64
		ValueMax  int64
		Items     []any       // lifetime: builder stage
		RgEntries RangePlList // lifetime: indexing data for retrieve
	}{
		Compiled:  l._compiled,
		MaxLen:    l.maxLen,
		AvgLen:    l.avgLen,
		ValueMin:  l.valueMin,
		ValueMax:  l.valueMax,
		RgEntries: l.rgEntries,
	}
	for e := l.items.Front(); e != nil; e = e.Next() {
		tmp.Items = append(tmp.Items, e.Value)
	}
	err := enc.Encode(tmp)
	return buf.Bytes(), err
}

func (l *RangeIdx) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	tmp := struct {
		Compiled  bool
		MaxLen    int // max length of Entries
		AvgLen    int // avg length of Entries
		ValueMin  int64
		ValueMax  int64
		Items     []any       // lifetime: builder stage
		RgEntries RangePlList // lifetime: indexing data for retrieve
	}{}
	err := dec.Decode(&tmp)
	if err != nil {
		return err
	}
	l._compiled = tmp.Compiled
	l.maxLen = tmp.MaxLen
	l.avgLen = tmp.AvgLen
	l.valueMin = tmp.ValueMin
	l.valueMax = tmp.ValueMax
	l.rgEntries = tmp.RgEntries
	l.items = list.New()
	for _, v := range tmp.Items {
		l.items.PushBack(v)
	}
	return nil
}

func (r *RangeEntries) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		Range *Range
		Ens   Entries
	}{
		Range: &r.Range,
		Ens:   r.entries,
	})
	return buf.Bytes(), err
}

func (r *RangeEntries) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	tmp := struct {
		Range *Range
		Ens   Entries
	}{}
	err := dec.Decode(&tmp)
	if err != nil {
		return err
	}
	if tmp.Range == nil {
		r.Range = Range{}
	} else {
		r.Range = *tmp.Range
	}
	r.entries = tmp.Ens
	return nil
}

func (r *Range) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		Left  int64
		Right int64
	}{
		Left:  r.left,
		Right: r.right,
	})
	return buf.Bytes(), err
}

func (r *Range) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	tmp := struct {
		Left  int64
		Right int64
	}{}
	err := dec.Decode(&tmp)
	if err != nil {
		return err
	}
	r.left = tmp.Left
	r.right = tmp.Right
	return nil
}
