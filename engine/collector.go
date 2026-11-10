package engine

import "github.com/echoface/be_indexer/core"

// prepareCollector gives ordinary collectors one callback per DocID. Built-in
// bitmap/TopK collectors own their deduplication, so they avoid an extra set.
func prepareCollector(collector core.ResultCollector) core.ResultCollector {
	if c, ok := collector.(core.DuplicateTolerantCollector); ok && c.AcceptsDuplicates() {
		return collector
	}
	u := &uniqueCollector{next: collector, seen: core.NewBitmapDocSet()}
	if c, ok := collector.(core.CompetitiveCollector); ok {
		return &competitiveUniqueCollector{uniqueCollector: u, competitive: c}
	}
	return u
}

type uniqueCollector struct {
	next core.ResultCollector
	seen *core.BitmapDocSet
}

func (*uniqueCollector) AcceptsDuplicates() bool { return true }
func (c *uniqueCollector) Add(id core.DocID) core.CollectResult {
	if c.seen.Contains(id) {
		return core.CollectContinue
	}
	c.seen.Add(id)
	return c.next.Add(id)
}

type competitiveUniqueCollector struct {
	*uniqueCollector
	competitive core.CompetitiveCollector
}

func (c *competitiveUniqueCollector) NextCandidate(entry core.EntryID) core.EntryID {
	return c.competitive.NextCandidate(entry)
}
