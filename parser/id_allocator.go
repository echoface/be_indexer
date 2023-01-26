package parser

import "hash/fnv"

type (
	IDAllocator interface {
		AllocStringID(v string) uint64
		FindStringID(v string) (value uint64, found bool)
	}

	// IDAllocatorImpl 用于将不同类型的值ID化，用于构造Index的PostingList，减少重复值
	IDAllocatorImpl struct {
		strBox map[string]uint64 //将string转变成紧凑的ID
	}

	HashAllocator struct {
		hashFn func(string) uint64
	}
)

func fnvHashString(s string) uint64 {
	fnv64 := fnv.New64()
	_, _ = fnv64.Write([]byte(s))
	return fnv64.Sum64()
}

func NewHashAllocator(fn func(string) uint64) *HashAllocator {
	alloc := &HashAllocator{
		hashFn: fnvHashString,
	}
	if fn != nil {
		alloc.hashFn = fn
	}
	return alloc
}

func (alloc *HashAllocator) FindStringID(v string) (value uint64, found bool) {
	return alloc.hashFn(v), true
}

func (alloc *HashAllocator) AllocStringID(v string) uint64 {
	return alloc.hashFn(v)
}

func NewIDAllocatorImpl() IDAllocator {
	return &IDAllocatorImpl{
		strBox: make(map[string]uint64),
	}
}

func (alloc *IDAllocatorImpl) TotalIDCount() uint64 {
	return uint64(len(alloc.strBox))
}

func (alloc *IDAllocatorImpl) FindStringID(v string) (value uint64, found bool) {
	value, found = alloc.strBox[v]
	return
}

func (alloc *IDAllocatorImpl) AllocStringID(v string) uint64 {
	if id, hit := alloc.strBox[v]; hit {
		return id
	}

	id := uint64(len(alloc.strBox))
	alloc.strBox[v] = id
	return id
}
