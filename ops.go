package ontology

// Semantics selects how multiplicities are treated by the set operations.
type Semantics int

const (
	// Set collapses duplicates: every value appears at most once in the
	// result, no matter how often it occurs in any input stream.
	Set Semantics = iota
	// Multiset keeps multiplicities: union takes the per-value maximum
	// count across streams, intersection the minimum, and difference the
	// first stream's count minus the sum of the others' counts (floored
	// at zero).
	Multiset
)

// Stats reports observable facts about a completed operation.
type Stats struct {
	Comparisons    int64 // comparisons performed during the merge
	Streams        int   // number of input streams
	TotalElements  int64 // sum of input stream lengths
	ResultElements int   // length of the returned slice
}

// Union returns every value that occurs in at least one stream.
// Zero streams yield a defined empty (non-nil) result.
func Union(streams [][]float64, sem Semantics) ([]float64, Stats, error) {
	emit := func(counts []int) int {
		n := 0
		for _, c := range counts {
			if c > n {
				n = c
			}
		}
		if sem == Set && n > 0 {
			return 1
		}
		return n
	}
	return run(streams, emit)
}

// Intersect returns every value that occurs in all streams. With zero
// streams the intersection is mathematically ambiguous, so ErrNoStreams
// is returned instead of a misleading empty slice.
func Intersect(streams [][]float64, sem Semantics) ([]float64, Stats, error) {
	if len(streams) == 0 {
		return nil, Stats{}, ErrNoStreams
	}
	emit := func(counts []int) int {
		n := counts[0]
		for _, c := range counts[1:] {
			if c < n {
				n = c
			}
		}
		if sem == Set && n > 0 {
			return 1
		}
		return n
	}
	return run(streams, emit)
}

// Difference returns the values of the first stream with the other
// streams removed. Under set semantics a value is kept (once) only if it
// occurs in stream 0 and in no other stream. Under multiset semantics its
// count is counts[0] minus the sum of all other counts, floored at zero.
// With zero streams there is no minuend, so ErrNoStreams is returned.
func Difference(streams [][]float64, sem Semantics) ([]float64, Stats, error) {
	if len(streams) == 0 {
		return nil, Stats{}, ErrNoStreams
	}
	emit := func(counts []int) int {
		others := 0
		present := false
		for _, c := range counts[1:] {
			others += c
			if c > 0 {
				present = true
			}
		}
		if sem == Set {
			if counts[0] > 0 && !present {
				return 1
			}
			return 0
		}
		if n := counts[0] - others; n > 0 {
			return n
		}
		return 0
	}
	return run(streams, emit)
}

// run drives a Merger and materializes the result chosen by emit, which
// maps a value's per-stream counts to its output multiplicity.
func run(streams [][]float64, emit func(counts []int) int) ([]float64, Stats, error) {
	m, err := NewMerger(streams)
	if err != nil {
		return nil, Stats{}, err
	}
	out := []float64{}
	var total int64
	for _, s := range streams {
		total += int64(len(s))
	}
	for {
		v, counts, ok := m.Next()
		if !ok {
			break
		}
		for n := emit(counts); n > 0; n-- {
			out = append(out, v)
		}
	}
	return out, Stats{
		Comparisons:    m.Comparisons(),
		Streams:        len(streams),
		TotalElements:  total,
		ResultElements: len(out),
	}, nil
}
