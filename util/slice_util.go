package util

func ContainInt(vs []int, t int) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func ContainUint(vs []uint, t uint) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func ContainInt32(vs []int32, t int32) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func ContainUint32(vs []uint32, t uint32) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func ContainInt64(vs []int64, t int64) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func ContainUint64(vs []uint64, t uint64) bool {
	for _, v := range vs {
		if v == t {
			return true
		}
	}
	return false
}

func DistinctInt(vs []int) (res []int) {
	m := map[int]struct{}{}
	for _, v := range vs {
		m[v] = struct{}{}
	}
	for v, _ := range m {
		res = append(res, v)
	}
	return res
}
