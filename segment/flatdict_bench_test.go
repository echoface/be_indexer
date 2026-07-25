package segment

import (
	"fmt"
	"math/rand"
	"testing"
)

func BenchmarkFlatDict_Find(b *testing.B) {
	sizes := []int{10_000, 100_000, 500_000, 1_000_000}
	scenarios := []struct {
		name string
		mode int // 0=best (always found), 1=worst (never found), 2=random
	}{
		{"hit", 0},
		{"miss", 1},
		{"random", 2},
	}

	for _, size := range sizes {
		for _, sc := range scenarios {
			name := fmt.Sprintf("size=%d/%s", size, sc.name)
			b.Run(name, func(b *testing.B) {
				m := make(map[string]PostingRef, size)
				keys := make([]string, 0, size)
				for i := 0; i < size; i++ {
					k := fmt.Sprintf("term_%08x", i)
					m[k] = PostingRef{Offset: uint64(i * 100), Count: uint32(i % 10)}
					keys = append(keys, k)
				}

				buf := WriteFlatDict(m)
				dict, err := NewFlatDict(buf)
				if err != nil {
					b.Fatal(err)
				}

				b.ResetTimer()
				b.ReportAllocs()

				switch sc.mode {
				case 0: // always hit - pick middle for balanced binary search
					term := []byte(keys[size/2])
					for i := 0; i < b.N; i++ {
						_, _ = dict.Find(term)
					}
				case 1: // always miss
					term := []byte("zzzz_nonexistent")
					for i := 0; i < b.N; i++ {
						_, _ = dict.Find(term)
					}
				case 2: // random
					idx := make([]string, b.N)
					for i := 0; i < b.N; i++ {
						if rand.Intn(2) == 0 {
							idx[i] = keys[rand.Intn(size)]
						} else {
							idx[i] = fmt.Sprintf("zzz_nonexistent_%d", rand.Intn(size))
						}
					}
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						_, _ = dict.Find([]byte(idx[i]))
					}
				}
			})
		}
	}
}

func BenchmarkFlatDict_Build(b *testing.B) {
	sizes := []int{10_000, 100_000, 500_000}
	for _, size := range sizes {
		name := fmt.Sprintf("size=%d", size)
		b.Run(name, func(b *testing.B) {
			m := make(map[string]PostingRef, size)
			for i := 0; i < size; i++ {
				k := fmt.Sprintf("term_%08x", i)
				m[k] = PostingRef{Offset: uint64(i * 100), Count: uint32(i % 10)}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = WriteFlatDict(m)
			}
		})
	}
}
