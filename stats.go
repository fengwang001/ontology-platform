package ontology

// Stats summarizes a set in O(runs) time. RunsVisited reports how many
// runs the computation touched, so callers can verify that statistics
// scale with the number of runs, not the number of bits.
type Stats struct {
	Count       uint64 // cardinality; may reach 2^32 for a full domain
	Min         uint32 // smallest element (valid when Empty is false)
	Max         uint32 // largest element (valid when Empty is false)
	Empty       bool
	RunsVisited int
}

// Stats computes cardinality, minimum and maximum in a single O(runs)
// pass: Count sums run lengths (as uint64, so a 2^32-length run cannot
// overflow), Min reads the first run, Max reads the last run.
func (s *Set) Stats() Stats {
	runs := s.snapshot()
	st := Stats{Empty: len(runs) == 0, RunsVisited: len(runs)}
	if st.Empty {
		return st
	}
	st.Min = runs[0].start
	st.Max = runs[len(runs)-1].end
	for _, r := range runs {
		st.Count += uint64(r.end) - uint64(r.start) + 1
	}
	return st
}

// Count returns the cardinality of the set in O(runs) time.
func (s *Set) Count() uint64 {
	var n uint64
	for _, r := range s.snapshot() {
		n += uint64(r.end) - uint64(r.start) + 1
	}
	return n
}

// Min returns the smallest element, or false when the set is empty.
func (s *Set) Min() (uint32, bool) {
	runs := s.snapshot()
	if len(runs) == 0 {
		return 0, false
	}
	return runs[0].start, true
}

// Max returns the largest element, or false when the set is empty.
func (s *Set) Max() (uint32, bool) {
	runs := s.snapshot()
	if len(runs) == 0 {
		return 0, false
	}
	return runs[len(runs)-1].end, true
}

// RunCount returns the number of runs in the compressed representation.
func (s *Set) RunCount() int {
	return len(s.snapshot())
}
