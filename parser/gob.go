package parser

import (
	"bytes"
	"encoding/gob"
	"errors"
)

func init() {
	gob.Register(&CommonStrParser{})
	gob.Register(&HashAllocator{})
	gob.Register(&StrHashParser{})
}

func (h *HashAllocator) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		Name string
	}{
		Name: "HashAllocator",
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (h *HashAllocator) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	temp := struct {
		Name string
	}{}
	err := dec.Decode(&temp)
	if err != nil {
		return err
	}
	if temp.Name != "HashAllocator" {
		return errors.New("not HashAllocator")
	}
	h.hashFn = fnvHashString
	return nil
}
