package util

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func MinInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func MinUInt64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
