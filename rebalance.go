package ontology

// Rebalance reassigns every entry a fresh, evenly spaced short key while
// preserving the exact relative order of all elements.
//
// The new key slice is built completely before it is swapped in under the
// write lock, so the rebalance is atomic: it either takes full effect or
// no effect, and concurrent Snapshot readers observe either all old keys
// or all new keys, never a mixture.
func (s *Sequence) Rebalance() {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.entries)
	if n == 0 {
		return
	}
	keys := spreadKeys(n)
	next := make([]Entry, n)
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
