package ontology

// Statistics are computed from the run list only, never by walking
// individual bits. Each method also reports how many runs it visited,
// so callers can assert the cost is O(runs), not O(bits).

// Count returns the cardinality of the set and the number of runs
// visited while computing it.
func (b *Bitmap) Count() (uint64, int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var total uint64
	for _, r := range b.runs {
		total += r.length
	}
	return total, len(b.runs)
}

// Min returns the smallest element. ok is false for the empty set.
func (b *Bitmap) Min() (min uint32, ok bool, runsVisited int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.runs) == 0 {
		return 0, false, 0
	}
	return b.runs[0].start, true, 1
}

// Max returns the largest element. ok is false for the empty set.
func (b *Bitmap) Max() (max uint32, ok bool, runsVisited int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if len(b.runs) == 0 {
		return 0, false, 0
	}
	return uint32(b.runs[len(b.runs)-1].end()), true, 1
}
