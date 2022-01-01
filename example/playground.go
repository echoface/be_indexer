package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

type X struct {
	data []int64
}

var p = sync.Pool{
	New: func() interface{} {
		return &X{
			data: make([]int64, 0, 1024),
		}
	},
}

func New() *X {
	return p.Get().(*X)
}

func Release(x *X) {
	x.data = x.data[:0]
	fmt.Println("reset x, cap:", cap(x.data))
	p.Put(x)
}

func main() {

	runtime.GC()
	st := runtime.MemStats{}
	runtime.ReadMemStats(&st)
	fmt.Println("1 alloc:", st.Alloc, " heapAlloc:", st.HeapAlloc)

	var result []int64
	var result2 []int64

	x := New()
	for i := 0; i < 100; i++ {
		x.data = append(x.data, int64(i*2))
	}
	result = x.data

	runtime.GC()
	runtime.ReadMemStats(&st)
	fmt.Println("2 alloc:", st.Alloc, " heapAlloc:", st.HeapAlloc)

	Release(x)

	runtime.GC()
	runtime.ReadMemStats(&st)
	fmt.Println("3 alloc:", st.Alloc, " heapAlloc:", st.HeapAlloc)

	time.Sleep(time.Second)

	x = New()
	for i := 0; i < 100; i++ {
		x.data = append(x.data, -1)
	}
	result2 = x.data

	runtime.GC()
	runtime.ReadMemStats(&st)
	fmt.Println("3 alloc:", st.Alloc, " heapAlloc:", st.HeapAlloc)

	time.Sleep(time.Second)
	fmt.Println("result:", result)
	fmt.Println("result2:", result2)
}
