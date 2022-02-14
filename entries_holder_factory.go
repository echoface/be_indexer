package be_indexer

import "fmt"

type (
	HolderBuilder func() EntriesHolder
)

const (
	HolderNameDefault   = "default"
	HolderNameACMatcher = "ac_matcher"
)

var holderFactory = make(map[string]HolderBuilder)

func init() {
	_ = RegisterEntriesHolder(HolderNameACMatcher, func() EntriesHolder {
		return NewACEntriesHolder(ACHolderOption{QuerySep: " "})
	})
	_ = RegisterEntriesHolder(HolderNameDefault, NewDefaultEntriesHolder)
}

func NewEntriesHolder(name string) EntriesHolder {
	if fn, ok := holderFactory[name]; ok {
		return fn()
	}
	return nil
}

func HasHolderBuilder(name string) bool {
	_, ok := holderFactory[name]
	return ok
}

func RegisterEntriesHolder(name string, builder HolderBuilder) error {
	if _, ok := holderFactory[name]; ok {
		return fmt.Errorf("holder name:%s has already registered", name)
	}
	holderFactory[name] = builder
	return nil
}
