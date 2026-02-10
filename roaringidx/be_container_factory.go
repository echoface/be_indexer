package roaringidx

import (
	"github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/parser"
)

var containerFactory map[string]ContainerBuilderFunc

type (
	// ContainerBuilderFunc creates a BEContainerBuilder for a field
	ContainerBuilderFunc func(field be_indexer.BEField) BEContainerBuilder
)

const (
	ContainerNameDefault    = "default"
	ContainerNameDefaultStr = "default_str" // default container with StrHashParser
	ContainerNameAcMatch    = "ac_matcher"
)

func init() {
	containerFactory = make(map[string]ContainerBuilderFunc)

	// Default container with NumberParser
	containerFactory[ContainerNameDefault] = func(field be_indexer.BEField) BEContainerBuilder {
		meta := &FieldMeta{
			field:     field,
			Container: ContainerNameDefault,
		}
		return NewDefaultBEContainer(meta, parser.NewNumberParser())
	}

	// Default container with StrHashParser
	containerFactory[ContainerNameDefaultStr] = func(field be_indexer.BEField) BEContainerBuilder {
		meta := &FieldMeta{
			field:     field,
			Container: ContainerNameDefaultStr,
		}
		return NewDefaultBEContainer(meta, parser.NewStrHashParser())
	}

	// AC matcher container (no parser needed)
	containerFactory[ContainerNameAcMatch] = func(field be_indexer.BEField) BEContainerBuilder {
		meta := &FieldMeta{
			field:     field,
			Container: ContainerNameAcMatch,
		}
		return NewACBEContainer(meta, DefaultACContainerQueryJoinSep)
	}
	//containerFactory["bsi"] = func(field be_indexer.BEField) BEContainerBuilder {
	//	return NewBSIBEContainerBuilder(field)
	//}
}

func RegisterContainerBuilder(name string, builderFunc ContainerBuilderFunc) bool {
	if _, ok := containerFactory[name]; ok {
		return false
	}
	containerFactory[name] = builderFunc
	return true
}

func NewContainerBuilder(field be_indexer.BEField, containerType string) BEContainerBuilder {
	if fn, ok := containerFactory[containerType]; ok {
		return fn(field)
	}
	return nil
}
