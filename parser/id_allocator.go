package parser

type (
	IDAllocator interface {
		AllocStringID(v string) uint64
		FindStringID(v *string) (value uint64, found bool)
	}

	// IDAllocatorImpl 用于将不同类型的值ID化，用于构造Index的PostingList，减少重复值
	IDAllocatorImpl struct {
		strBox map[string]uint64 //将string转变成紧凑的ID
	}
)

var DefaultIDAllocator = NewIDAllocatorImpl()

func NewIDAllocatorImpl() IDAllocator {
	return &IDAllocatorImpl{
		strBox: make(map[string]uint64),
	}
}

func (alloc *IDAllocatorImpl) TotalIDCount() uint64 {
	return uint64(len(alloc.strBox))
}

func (alloc *IDAllocatorImpl) FindStringID(v *string) (value uint64, found bool) {
	if v == nil {
		return 0, false
	}
	value, found = alloc.strBox[*v]
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
