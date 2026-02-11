package core

import (
	"fmt"
	"strings"

	"github.com/echoface/be_indexer/util"
)

type (
	// EntriesContainer for default entries Holder, it can hold different field's entries,
	// but for ACMatcher or other Holder, it may only hold entries for one field
	EntriesContainer struct {
		DefaultIndex FieldIndex // shared entries holder

		FieldIndices map[BEField]FieldIndex // special field holder
	}

	EntriesContainerBuilder struct {
		DefaultBuilder FieldIndexBuilder // shared entries holder

		FieldBuilders map[BEField]FieldIndexBuilder // special field holder
	}
)

func NewEntriesContainerBuilder() *EntriesContainerBuilder {
	return &EntriesContainerBuilder{
		DefaultBuilder: NewFieldBuilder(HolderNameDefault),
		FieldBuilders:  map[BEField]FieldIndexBuilder{},
	}
}

func NewEntriesContainer() *EntriesContainer {
	return &EntriesContainer{
		DefaultIndex: NewFieldIndex(HolderNameDefault),
		FieldIndices: map[BEField]FieldIndex{},
	}
}

func (c *EntriesContainerBuilder) CompileEntries() (*EntriesContainer, error) {
	defHolder, err := c.DefaultBuilder.CompileEntries()
	if err != nil {
		return nil, err
	}

	fieldHolders := make(map[BEField]FieldIndex, len(c.FieldBuilders))
	for k, v := range c.FieldBuilders {
		fh, err := v.CompileEntries()
		if err != nil {
			return nil, err
		}
		fieldHolders[k] = fh
	}

	return &EntriesContainer{
		DefaultIndex: defHolder,
		FieldIndices: fieldHolders,
	}, nil
}

func (c *EntriesContainerBuilder) GetFieldBuilder(desc *FieldDesc) FieldIndexBuilder {
	if desc.Container == HolderNameDefault {
		return c.DefaultBuilder
	}
	if holder, ok := c.FieldBuilders[desc.Field]; ok {
		return holder
	}
	return nil
}

func (c *EntriesContainerBuilder) CreateFieldBuilder(desc *FieldDesc) FieldIndexBuilder {
	holder := c.GetFieldBuilder(desc)
	if holder != nil { // already has holder for field
		return holder
	}
	hasBuilder := HasFieldBuilder(desc.Container)
	util.PanicIf(!hasBuilder, "field:%s container:%s not found, plz register it", desc.Field, desc.Container)

	holder = NewFieldBuilder(desc.Container)
	c.FieldBuilders[desc.Field] = holder

	return holder
}

func (c *EntriesContainer) GetFieldIndexData(desc *FieldDesc) FieldIndex {
	if desc.Container == HolderNameDefault {
		return c.DefaultIndex
	}
	if holder, ok := c.FieldIndices[desc.Field]; ok {
		return holder
	}
	return nil
}

// DumpInfo
// default holder: {name:%s value_count:%d, max_entries:%d avg_entries:%d}
// field holder:
//
//	>field:%s {name: %s, value_count:%d max_entries:%d avg_entries:%d}
//	>field:%s {name: %s, value_count:%d max_entries:%d avg_entries:%d}
func (c *EntriesContainer) DumpInfo(buf *strings.Builder) {
	buf.WriteString("holder information:")
	c.DefaultIndex.DumpInfo(buf)
	for field, holder := range c.FieldIndices {
		buf.WriteString(fmt.Sprintf("  >field:%s ", field))
		holder.DumpInfo(buf)
		buf.WriteString("\n")
	}
}
