package be_indexer

type (
	HolderBuilder func() EntriesHolder
)

const (
	HolderNameDefault     = "default"
	HolderNameACMatcher   = "ac_matcher"
	HolderNameExtendRange = "ext_range"
)

var holderFactory = make(map[string]HolderBuilder)

func init() {
	RegisterEntriesHolder(HolderNameDefault, func() EntriesHolder {
		return NewDefaultEntriesHolder()
	})
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

func RegisterEntriesHolder(name string, builder HolderBuilder) {
	if HasHolderBuilder(name) {
		LogInfo("holder name:%s already registered", name)
	}
	holderFactory[name] = builder
}
