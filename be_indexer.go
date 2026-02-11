package be_indexer

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/echoface/be_indexer/core"
)

const (
	WildcardFieldName = core.WildcardFieldName
)

var wildcardTerm = NewTerm(WildcardFieldName, 0)

type (
	indexBase struct {
		// fieldsData a field settings and resource, if not configured, it will use default parser and container
		// for expression values;
		fieldsData map[core.BEField]*core.FieldDesc

		// wildcardEntries hold all entry id that conjunction size is zero;
		wildcardEntries core.Entries
	}
)

func (bi *indexBase) AddWildcardEID(id core.EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

func (bi *indexBase) SetFieldDesc(fieldsData map[core.BEField]*core.FieldDesc) {
	bi.fieldsData = fieldsData
}

func (bi *indexBase) sortWildcards() {
	if len(bi.wildcardEntries) > 0 {
		sort.Sort(bi.wildcardEntries)
	}
}

func (bi *indexBase) GetSegment() (core.SegmentReader, error) {
	return nil, fmt.Errorf("GetSegment not supported for this indexer type")
}

// collectorPool default collect pool
var collectorPool = sync.Pool{
	New: func() interface{} {
		return NewDocIDCollector()
	},
}

func PickCollector() *DocIDCollector {
	return collectorPool.Get().(*DocIDCollector)
}

func PutCollector(c *DocIDCollector) {
	if c == nil {
		return
	}
	c.Reset()
	collectorPool.Put(c)
}

func WithStepDetail() core.IndexOpt {
	return func(ctx *core.RetrieveContext) {
		ctx.DumpStepInfo = true
	}
}

// WithCollector specify a user defined collector
func WithCollector(fn core.ResultCollector) core.IndexOpt {
	return func(ctx *core.RetrieveContext) {
		ctx.Collector = fn
	}
}

func newRetrieveCtx(ass core.Assignments, opts ...core.IndexOpt) core.RetrieveContext {
	ctx := core.RetrieveContext{}
	ctx.Assigns = ass
	for _, fn := range opts {
		fn(&ctx)
	}
	return ctx
}

func PrintIndexInfo(index core.BEIndex) {
	if index == nil {
		fmt.Println("nil indexer")
	}
	sb := &strings.Builder{}
	index.DumpIndexInfo(sb)
	fmt.Println(sb.String())
}
