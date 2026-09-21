package streamset

// Compute merges the given ascending streams once and applies op under
// the requested semantics. It returns a freshly allocated result slice
// (empty but non-nil when nothing qualifies), merge statistics, and any
// validation error. Inputs are never modified.
func Compute(op Op, sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	if op == OpIntersect && len(streams) == 0 {
		return nil, Stats{}, ErrEmptyIntersection
	}
	m, err := NewMerger(streams)
	if err != nil {
		return nil, Stats{}, err
	}
	out := make([]float64, 0)
	for {
		value, counts, ok := m.Next()
		if !ok {
			break
		}
		for n := outputCount(op, sem, counts); n > 0; n-- {
			out = append(out, value)
		}
	}
	return out, m.Stats(), nil
}

// Union computes the union of the streams under the given semantics.
func Union(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return Compute(OpUnion, sem, streams...)
}

// Intersect computes the intersection of the streams under the given
// semantics. With zero streams it returns ErrEmptyIntersection.
func Intersect(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return Compute(OpIntersect, sem, streams...)
}

// Difference computes streams[0] minus the remaining streams under the
// given semantics. With zero streams the result is empty.
func Difference(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return Compute(OpDifference, sem, streams...)
}

// outputCount decides how many times a value with the given per-stream
// occurrence counts appears in the result.
func outputCount(op Op, sem Semantics, counts []int) int {
	if sem == Multiset {
		return multisetCount(op, counts)
	}
	return setCount(op, counts)
}

func setCount(op Op, counts []int) int {
	switch op {
	case OpUnion:
		for _, c := range counts {
			if c > 0 {
				return 1
			}
		}
	case OpIntersect:
		for _, c := range counts {
			if c == 0 {
				return 0
			}
		}
		return 1
	case OpDifference:
		if len(counts) == 0 || counts[0] == 0 {
			return 0
		}
		for _, c := range counts[1:] {
			if c > 0 {
				return 0
			}
		}
		return 1
	}
	return 0
}

func multisetCount(op Op, counts []int) int {
	switch op {
	case OpUnion:
		max := 0
		for _, c := range counts {
			if c > max {
				max = c
			}
		}
		return max
	case OpIntersect:
		if len(counts) == 0 {
			return 0
		}
		min := counts[0]
		for _, c := range counts[1:] {
			if c < min {
				min = c
			}
		}
		return min
	case OpDifference:
		if len(counts) == 0 {
			return 0
		}
		n := counts[0]
		for _, c := range counts[1:] {
			n -= c
		}
		if n < 0 {
			return 0
		}
		return n
	}
	return 0
}
