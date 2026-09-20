package alloc

// Split divides amount equally into n parts and is equivalent to
// Allocate with n equal weights: the first amount mod n parts (by
// index) receive one extra unit. It satisfies the same invariants as
// Allocate. n <= 0 yields ErrInvalidParts.
func Split(amount int64, n int) ([]int64, error) {
	if n <= 0 {
		return nil, ErrInvalidParts
	}
	neg := amount < 0
	mag := uint64(amount)
	if neg {
		mag = 0 - mag
	}
	un := uint64(n)
	quot, extra := mag/un, mag%un
	out := make([]int64, n)
	for i := range out {
		v := quot
		if uint64(i) < extra {
			v++
		}
		if neg {
			out[i] = int64(0 - v)
		} else {
			out[i] = int64(v)
		}
	}
	return out, nil
}
