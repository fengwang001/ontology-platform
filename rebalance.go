package ontology

// Rebalance reassigns every entry a fresh, evenly spaced short key while
// preserving the exact relative order of all elements.
//
// The new keys are computed for the sequence length observed under the
// write lock, so the rebalance is atomic: it either takes full effect or
// no effect, and concurrent Snapshot readers observe either all old keys
// or all new keys, never a mixture.
func (s *Sequence) Rebalance() {
	// Fast path: size the sequence under the read lock and compute the
	// new keys outside any lock, so the write lock is held for as short
	// a time as possible.
	s.mu.RLock()
	n := len(s.entries)
	s.mu.RUnlock()
	if n == 0 {
		return
	}
	keys := spreadKeys(n)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) != n {
		// Inserts landed between the sizing read above and this write
		// lock, so the precomputed keys no longer cover every entry.
		// The old code kept the stale keys and left the tail entries
		// with their old-generation keys, mixing long and short keys
		// and breaking strict key ordering. Recompute for the length
		// actually observed under the write lock instead.
		keys = spreadKeys(len(s.entries))
	}
	next := make([]Entry, len(s.entries))
	for i, e := range s.entries {
		next[i] = Entry{Key: keys[i], Value: e.Value}
	}
	s.entries = next
}

// spreadKeys returns n strictly increasing keys of equal width. The
// digits are charset[1:], so no generated key ever ends with the
// reserved firstChar and every gap stays insertable.
func spreadKeys(n int) []string {
	digits := charset[1:]
	width := 1
	for pow(len(digits), width) < n+1 {
		width++
	}
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = encodeFixed(i+1, digits, width)
	}
	return keys
}

// pow returns base**exp for small non-negative exponents.
func pow(base, exp int) int {
	p := 1
	for i := 0; i < exp; i++ {
		p *= base
	}
	return p
}

// encodeFixed renders v in the given digit alphabet, left-padded with the
// smallest digit to exactly width bytes.
func encodeFixed(v int, digits string, width int) string {
	base := len(digits)
	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		buf[i] = digits[v%base]
		v /= base
	}
	return string(buf)
}
