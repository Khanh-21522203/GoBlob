package util

// MinUint64 returns the minimum of two uint64 values.
func MinUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// MaxUint64 returns the maximum of two uint64 values.
func MaxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// MinInt returns the minimum of two int values.
func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MaxInt returns the maximum of two int values.
func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// MinInt32 returns the minimum of two int32 values.
func MinInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// MaxInt32 returns the maximum of two int32 values.
func MaxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
