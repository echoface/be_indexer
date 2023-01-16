package be_indexer

type (
	// CacheProvider a interface
	CacheProvider interface {
		// expire all existing cache data
		// when field config change or full-build mode will triggle this
		Reset()

		Get(conjID ConjID) ([]byte, bool)

		Set(conjID ConjID, data []byte)
	}
)
