package ontology

// Union merges all streams and emits every value that appears in at
// least one stream. Zero streams yield the empty result.
func Union(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return compute(OpUnion, sem, streams)
}

// Intersect merges all streams and emits every value that appears in
// all of them. Zero streams are rejected with ErrEmptyIntersect.
func Intersect(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return compute(OpIntersect, sem, streams)
}

// Difference merges all streams and emits values of the first stream
// not covered by the remaining streams. Zero streams are rejected with
// ErrEmptyDifference.
func Difference(sem Semantics, streams ...[]float64) ([]float64, Stats, error) {
	return compute(OpDifference, sem, streams)
}

// compute runs a single k-way merge over the streams, groups equal
// values, and folds each group into the result according to op and sem.
// The result is freshly allocated; the inputs are never modified.
func compute(op Op, sem Semantics, streams [][]float64) ([]float64, Stats, error) {
	if len(streams) == 0 {
		switch op {
		case OpUnion:
			return []float64{}, Stats{}, nil
		case OpIntersect:
			return nil, Stats{}, ErrEmptyIntersect
		default:
			return nil, Stats{}, ErrEmptyDifference
		}
	}

	m, err := newMerger(streams)
	if err != nil {
		return nil, Stats{}, err
	}

	result := []float64{}
	counts := make([]int, len(streams))
	inGroup := false
	var current float64

	flush := func() {
		result = emitGroup(result, op, sem, current, counts)
		for i := range counts {
			counts[i] = 0
		}
	}

	for {
		it, ok := m.pop()
		if !ok {
			break
		}
		if inGroup && it.value != current {
			flush()
			inGroup = false
		}
		if !inGroup {
			current = it.value
			inGroup = true
		}
		counts[it.stream]++
	}
	if inGroup {
		flush()
	}

	return result, Stats{Comparisons: m.heap.comparisons, MaxHeapSize: m.maxHeap}, nil
}

// emitGroup appends one grouped value to result according to the
// operation and semantics. counts[i] is the multiplicity of value in
// stream i.
func emitGroup(result []float64, op Op, sem Semantics, value float64, counts []int) []float64 {
	return appendTimes(result, value, groupMultiplicity(op, sem, counts))
}

func groupMultiplicity(op Op, sem Semantics, counts []int) int {
	switch op {
	case OpUnion:
		if sem == Set {
			return 1
		}
		max := 0
		for _, c := range counts {
			if c > max {
				max = c
			}
		}
		return max
	case OpIntersect:
		min := counts[0]
		for _, c := range counts[1:] {
			if c < min {
				min = c
			}
		}
		if sem == Set && min > 0 {
			return 1
		}
		return min
	default: // Difference
		d := counts[0]
		for _, c := range counts[1:] {
			d -= c
		}
		if d < 0 {
			d = 0
		}
		if sem == Set && d > 0 {
			return 1
		}
		return d
	}
}

func appendTimes(result []float64, value float64, times int) []float64 {
	for ; times > 0; times-- {
		result = append(result, value)
	}
	return result
}
