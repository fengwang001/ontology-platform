package ontology

func dup(x int, n int, tail []int) []int {
	out := make([]int, 0, n+len(tail))
	for i := 0; i < n; i++ {
		out = append(out, x)
	}
	return append(out, tail...)
}

// dupAfter yields tail followed by n copies of x.
func dupAfter(tail []int, x int, n int) []int {
	out := make([]int, 0, n+len(tail))
	out = append(out, tail...)
	for i := 0; i < n; i++ {
		out = append(out, x)
	}
	return out
}

func dupu(x uint32, n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = x
	}
	return out
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func u32Equal(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lenUvarint(v int) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// tieSequence builds rows values using exactly `distinct` distinct uint32
// values, all appearing at least once, while keeping every equal-run shorter
// than 8 so the indices stay in one bit-packed run.
func tieSequence(rows, distinct int) []uint32 {
	out := make([]uint32, rows)
	for i := 0; i < distinct; i++ {
		out[i] = uint32(i + 1)
	}
	for i := distinct; i < rows; i++ {
		out[i] = uint32((i % (distinct - 1)) + 1)
	}
	return out
}
