package ontology

// locate finds the run covering position p. It returns the run index and
// the absolute start position of that run. found is false when p lies
// past the end of the last run (implicit zero region); in that case idx
// is len(runs) and start is the total covered length.
//
// Caller must hold at least the read lock. Runs in O(number of runs).
func (s *Set) locate(p uint64) (idx int, start uint64, found bool) {
	var pos uint64
	for i, r := range s.runs {
		if p < pos+r.length {
			return i, pos, true
		}
		pos += r.length
	}
	return len(s.runs), pos, false
}

// Contains reports whether pos is a member of the set.
func (s *Set) Contains(pos uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, _, found := s.locate(uint64(pos))
	return found && s.runs[idx].val == 1
}

// Set adds pos to the set. It is idempotent: setting a bit that is
// already 1 leaves the encoding byte-for-byte unchanged.
func (s *Set) Set(pos uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := uint64(pos)
	idx, start, found := s.locate(p)
	if !found {
		// Past the last run: implicit zeros up to p, then a 1-bit.
		// A zero-length gap (p == start) is removed by normalize,
		// which then merges the new 1-bit into the trailing 1-run.
		s.runs = normalize(append(s.runs, run{0, p - start}, run{1, 1}))
		return
	}
	r := s.runs[idx]
	if r.val == 1 {
		return // already set, no change
	}
	off := p - start
	next := make([]run, 0, len(s.runs)+2)
	next = append(next, s.runs[:idx]...)
	next = append(next, run{0, off}, run{1, 1}, run{0, r.length - off - 1})
	next = append(next, s.runs[idx+1:]...)
	s.runs = normalize(next)
}

// Clear removes pos from the set. It is idempotent: clearing a bit that
// is already 0 leaves the encoding byte-for-byte unchanged.
func (s *Set) Clear(pos uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := uint64(pos)
	idx, start, found := s.locate(p)
	if !found {
		return // implicit zero region, no change
	}
	r := s.runs[idx]
	if r.val == 0 {
		return // already clear, no change
	}
	off := p - start
	next := make([]run, 0, len(s.runs)+2)
	next = append(next, s.runs[:idx]...)
	next = append(next, run{1, off}, run{0, 1}, run{1, r.length - off - 1})
	next = append(next, s.runs[idx+1:]...)
	s.runs = normalize(next)
}
