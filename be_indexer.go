package be_indexer

import (
	"sync"

	"github.com/echoface/be_indexer/parser"
)

const (
	WildcardFieldName = BEField("_Z_")
)

var (
	wildcardQKey = newQKey(WildcardFieldName, 0)
)

type (
	FieldDesc struct {
		ID     uint64
		Field  BEField
		Parser parser.FieldValueParser
		option FieldOption
	}

	FieldOption struct {
		Parser    string
		Container string // specific the holder|container all tokenized value and correspondent Entries
	}

	IndexerSettings struct {
		EnableDebugMode bool
		FieldConfig     map[BEField]FieldOption
	}

	retrieveContext struct {
		retrieveOption
		assigns Assignments
	}

	retrieveOption struct {
		dumpStepInfo bool

		dumpEntriesDetail bool

		userCollector bool

		collector ResultCollector
	}

	IndexOpt func(ctx *retrieveContext)

	BEIndex interface {
		// addWildcardEID interface used by builder
		addWildcardEID(id EntryID)

		// set a field desc
		setFieldDesc(fieldsData map[BEField]*FieldDesc)

		// newContainer indexer need return a valid Container for k size
		newContainer(k int) *fieldEntriesContainer

		// compileIndexer prepare indexer and optimize index data
		compileIndexer()

		// Retrieve scan index data and retrieve satisfied document
		// NOTE: when use a customized Collector, it will return nil/empty result for performance reason
		Retrieve(queries Assignments, opt ...IndexOpt) (result DocIDList, err error)

		// DumpEntries debug api
		DumpEntries() string

		DumpEntriesSummary() string
	}

	indexBase struct {
		// fieldsData a field settings and resource, if not configured, it will use default parser and container
		// for expression values;
		fieldsData map[BEField]*FieldDesc

		// wildcardEntries hold all entry id that conjunction size is zero;
		wildcardEntries Entries
	}
)

func (bi *indexBase) setFieldDesc(fieldsData map[BEField]*FieldDesc) {
	bi.fieldsData = fieldsData
}

// addWildcardEID append wildcard entry id to Z set
func (bi *indexBase) addWildcardEID(id EntryID) {
	bi.wildcardEntries = append(bi.wildcardEntries, id)
}

// collectorPool default collect pool
var collectorPool = sync.Pool{
	New: func() interface{} {
		return NewDocIDCollector()
	},
}

func newCollector() *DocIDCollector {
	return collectorPool.Get().(*DocIDCollector)
}

func reuseCollector(c *DocIDCollector) {
	c.Reset()
	collectorPool.Put(c)
}

func WithStepDetail() IndexOpt {
	return func(ctx *retrieveContext) {
		ctx.dumpStepInfo = true
	}
}

func WithDumpEntries() IndexOpt {
	return func(ctx *retrieveContext) {
		ctx.dumpEntriesDetail = true
	}
}

// WithCollector specific a user defined collector
func WithCollector(fn ResultCollector) IndexOpt {
	if fn == nil {
		return func(*retrieveContext) {}
	}

	return func(ctx *retrieveContext) {
		ctx.collector = fn
		ctx.userCollector = true
	}
}

func (ctx *retrieveContext) Reset() {
	ctx.collector = nil
	ctx.userCollector = false
	ctx.dumpStepInfo = false
	ctx.dumpEntriesDetail = false
}

// newRetrieveCtx don't export pool outside client
func newRetrieveCtx(assigns Assignments, opts ...IndexOpt) retrieveContext {
	var ctx retrieveContext
	ctx.assigns = assigns
	for _, fn := range opts {
		fn(&ctx)
	}
	if !ctx.userCollector {
		ctx.collector = newCollector()
	}
	return ctx
}
