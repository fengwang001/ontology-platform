package ontology

// Union merges all streams. Set semantics emits each distinct value
// once; Multiset semantics emits each value max(counts) times.
// With zero streams it returns an empty, non-nil slice.
func (m *Merger) Union(sem Semantics) ([]float64, error) {
	return m.compute(func(counts []int) int {
		if sem == Set {
			return 1
		}
		max := 0
		for _, n := range counts {
			if n > max {
				max = n
			}
		}
		return max
	})
}

// Intersect keeps only values present in every stream. Set semantics
// emits each such value once; Multiset semantics emits it min(counts)
// times. With zero streams it returns ErrNoStreams.
func (m *Merger) Intersect(sem Semantics) ([]float64, error) {
	if len(m.streams) == 0 {
		return nil, ErrNoStreams
	}
	return m.compute(func(counts []int) int {
		min := -1
		for _, n := range counts {
			if n == 0 {
				return 0
			}
			if min < 0 || n < min {
				min = n
			}
		}
		if sem == Set {
			return 1
		}
		return min
	})
}

// Difference keeps values of the first stream that the other streams
// do not cover. Set semantics emits a value once when it occurs in the
// first stream and in no other; Multiset semantics emits it
// max(0, counts[0]-sum(counts[1:])) times. With zero streams it
// returns an empty, non-nil slice.
func (m *Merger) Difference(sem Semantics) ([]float64, error) {
	if len(m.streams) == 0 {
		return []float64{}, nil
	}
	return m.compute(func(counts []int) int {
		if sem == Set {
			if counts[0] == 0 {
				return 0
			}
			for _, n := range counts[1:] {
				if n > 0 {
					return 0
				}
			}
			return 1
		}
		d := counts[0]
		for _, n := range counts[1:] {
			d -= n
		}
		if d < 0 {
			return 0
		}
		return d
	})
}
