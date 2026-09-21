package ontology

// Semantics selects between set and multiset (bag) behavior.
type Semantics int

const (
	// Set emits each distinct value at most once.
	Set Semantics = iota
	// Multiset emits each distinct value according to per-stream
	// occurrence counts (see Union, Intersect, Difference).
	Multiset
)

// rule decides how many times a distinct value is emitted, given its
// occurrence count in each stream.
type rule func(counts []int) int

func apply(sem Semantics, r rule, streams [][]float64) ([]float64, error) {
	m, err := NewMerger(streams)
	if err != nil {
		return nil, err
	}
	out := make([]float64, 0)
	for {
		v, counts, ok := m.Next()
		if !ok {
			break
		}
		n := r(counts)
		if sem == Set && n > 0 {
			n = 1
		}
		for ; n > 0; n-- {
			out = append(out, v)
		}
	}
	return out, nil
}

// Union returns the union of the streams. Under Multiset semantics each
// distinct value appears max(counts) times. Zero streams yield an empty
// result.
func Union(sem Semantics, streams ...[]float64) ([]float64, error) {
	return apply(sem, func(counts []int) int {
		max := 0
		for _, c := range counts {
			if c > max {
				max = c
			}
		}
		return max
	}, streams)
}

// Intersect returns the intersection of the streams. Under Multiset
// semantics each distinct value appears min(counts) times. Calling it
// with zero streams returns ErrNoStreams.
func Intersect(sem Semantics, streams ...[]float64) ([]float64, error) {
	if len(streams) == 0 {
		return nil, ErrNoStreams
	}
	return apply(sem, func(counts []int) int {
		min := counts[0]
		for _, c := range counts[1:] {
			if c < min {
				min = c
			}
		}
		return min
	}, streams)
}

// Difference returns the first stream minus the remaining streams.
// Under Multiset semantics each distinct value appears
// max(0, counts[0]-sum(counts[1:])) times. Zero streams yield an empty
// result.
func Difference(sem Semantics, streams ...[]float64) ([]float64, error) {
	return apply(sem, func(counts []int) int {
		n := counts[0]
		for _, c := range counts[1:] {
			n -= c
		}
		if n < 0 {
			n = 0
		}
		return n
	}, streams)
}
