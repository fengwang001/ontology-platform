package ontology

// Count returns the cardinality of the set. It sums the lengths of the
// 1-runs only, so it runs in O(number of runs), never O(bits).
func (s *Set) Count() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n uint64
	for _, r := range s.runs {
		if r.val == 1 {
			n += r.length
		}
	}
	s.statRuns = len(s.runs)
	return n
}

// Min returns the smallest member of the set, or false if empty.
// It scans runs, not bits.
func (s *Set) Min() (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var start uint64
	for i, r := range s.runs {
		if r.val == 1 {
			s.statRuns = i + 1
			return uint32(start), true
		}
		start += r.length
	}
	s.statRuns = len(s.runs)
	return 0, false
}

// Max returns the largest member of the set, or false if empty. The last
// run is always a 1-run (no trailing zero runs), so this is O(runs) for
// the length sum and visits a single run of interest.
func (s *Set) Max() (uint32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		s.statRuns = 0
		return 0, false
	}
	var total uint64
	for _, r := range s.runs {
		total += r.length
	}
	s.statRuns = 1
	return uint32(total - 1), true
}

// LastStatRuns reports how many runs the most recent Count, Min or Max
// call visited. It is always O(runs), never O(bits).
func (s *Set) LastStatRuns() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statRuns
}
