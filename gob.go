package be_indexer

import (
	"bytes"
	"encoding/gob"
	"errors"

	"git.ushareit.me/corekit/adindex_v2/be_indexer/parser"
)

// indexBaseExported 是一个辅助结构体，用于包装 indexBase 的数据
type indexBaseExported struct {
	FieldsData      map[BEField]*FieldDesc
	WildcardEntries Entries
	KSizeContainers []*EntriesContainer
}

func init() {
	// 注册所有需要序列化的类型
	gob.Register(indexBase{})
	gob.Register(&CompactBEIndex{})
	gob.Register(&KGroupsBEIndex{})
	gob.Register(&EntriesContainer{})
	gob.Register(FieldDesc{})
	gob.Register(Entries{})
	gob.Register(&DefaultEntriesHolder{})
}

func (bi *KGroupsBEIndex) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(indexBaseExported{
		FieldsData:      bi.fieldsData,
		WildcardEntries: bi.wildcardEntries,
		KSizeContainers: bi.kSizeContainers,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (bi *KGroupsBEIndex) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	temp := indexBaseExported{}
	err := dec.Decode(&temp)
	if err != nil {
		return err
	}
	bi.fieldsData = temp.FieldsData
	bi.wildcardEntries = temp.WildcardEntries
	bi.kSizeContainers = temp.KSizeContainers
	return nil
}

func (bi *indexBase) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		FieldsData      map[BEField]*FieldDesc
		WildcardEntries Entries
	}{
		FieldsData:      bi.fieldsData,
		WildcardEntries: bi.wildcardEntries,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (bi *indexBase) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	temp := struct {
		FieldsData      map[BEField]*FieldDesc
		WildcardEntries Entries
	}{}
	err := dec.Decode(&temp)
	if err != nil {
		return err
	}
	bi.fieldsData = temp.FieldsData
	bi.wildcardEntries = temp.WildcardEntries
	return nil
}

func (ec *EntriesContainer) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		DefaultHolder EntriesHolder
		FieldHolder   map[BEField]EntriesHolder
	}{
		DefaultHolder: ec.defaultHolder,
		FieldHolder:   ec.fieldHolder,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (ec *EntriesContainer) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	temp := struct {
		DefaultHolder EntriesHolder
		FieldHolder   map[BEField]EntriesHolder
	}{}
	err := dec.Decode(&temp)
	if err != nil {
		return err
	}
	ec.defaultHolder = temp.DefaultHolder
	ec.fieldHolder = temp.FieldHolder
	return nil
}

func (d *DefaultEntriesHolder) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(struct {
		Debug     bool
		MaxLen    int64 // max length of Entries
		AvgLen    int64 // avg length of Entries
		PlEntries map[Term]Entries

		Parser      parser.FieldValueParser
		FieldParser map[BEField]parser.FieldValueParser
	}{
		Debug:       d.debug,
		MaxLen:      d.maxLen,
		AvgLen:      d.avgLen,
		PlEntries:   d.plEntries,
		Parser:      d.Parser,
		FieldParser: d.FieldParser,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *DefaultEntriesHolder) GobDecode(data []byte) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	temp := struct {
		Debug     bool
		MaxLen    int64 // max length of Entries
		AvgLen    int64 // avg length of Entries
		PlEntries map[Term]Entries

		Parser      parser.FieldValueParser
		FieldParser map[BEField]parser.FieldValueParser
	}{}
	err := dec.Decode(&temp)
	if err != nil {
		return err
	}
	d.debug = temp.Debug
	d.maxLen = temp.MaxLen
	d.avgLen = temp.AvgLen
	d.plEntries = temp.PlEntries
	d.Parser = temp.Parser
	d.FieldParser = temp.FieldParser
	return nil
}

func (c *CompactBEIndex) GobEncode() ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (c *CompactBEIndex) GobDecode(data []byte) error {
	return errors.New("not implemented")
}
