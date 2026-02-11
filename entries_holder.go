package be_indexer

import (
	"github.com/echoface/be_indexer/core"
)

const (
	HolderNameDefault     = core.HolderNameDefault
	HolderNameACMatcher   = core.HolderNameACMatcher
	HolderNameExtendRange = core.HolderNameExtendRange
)

// RegisterField registers both builder and index factories for a field type.
// This is the recommended way to register field implementations.
func RegisterField(name string, impl core.FieldImplementation) {
	core.RegisterField(name, impl)
}

func NewFieldBuilder(name string) core.FieldIndexBuilder {
	return core.NewFieldBuilder(name)
}

func NewFieldIndex(name string) core.FieldIndex {
	return core.NewFieldIndex(name)
}

func HasFieldBuilder(name string) bool {
	return core.HasFieldBuilder(name)
}

func RegisterFieldBuilder(name string, builder core.FieldBuilderFactory) {
	core.RegisterFieldBuilder(name, builder)
}

func RegisterFieldIndex(name string, builder core.FieldIndexFactory) {
	core.RegisterFieldIndex(name, builder)
}
