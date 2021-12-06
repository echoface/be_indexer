package parser

type (
	IDAllocator interface {
		AllocNumID(v int64) uint64
		AllocStringID(v string) uint64
		FindNumID(v int64) (value uint64, found bool)
		FindStringID(v *string) (value uint64, found bool)
	}
	/*IDAllocatorImpl 用于将不同类型的值ID化，用于构造Index的PostingList，减少重复值*/
	IDAllocatorImpl struct {
		numBox map[int64]uint64  //用于将整形数字重新安排
		strBox map[string]uint64 //将string转变成紧凑的ID
	}
)

func NewIDAllocatorImpl() IDAllocator {
	return &IDAllocatorImpl{
		numBox: make(map[int64]uint64),
		strBox: make(map[string]uint64),
	}
}

func (alloc *IDAllocatorImpl) TotalIDCount() uint64 {
	return uint64(len(alloc.strBox)) + uint64(len(alloc.numBox))
}

func (alloc *IDAllocatorImpl) FindNumID(v int64) (value uint64, found bool) {
	value, found = alloc.numBox[v]
	return
}

func (alloc *IDAllocatorImpl) FindStringID(v *string) (value uint64, found bool) {
	if v == nil {
		return 0, false
	}
	value, found = alloc.strBox[*v]
	return
}

func (alloc *IDAllocatorImpl) AllocNumID(v int64) uint64 {
	if id, hit := alloc.numBox[v]; hit {
		return id
	}
	id := uint64(len(alloc.numBox))
	alloc.numBox[v] = id
	return id
}

func (alloc *IDAllocatorImpl) AllocStringID(v string) uint64 {
	if id, hit := alloc.strBox[v]; hit {
		return id
	}

	id := uint64(len(alloc.strBox))
	alloc.strBox[v] = id
	return id
}
