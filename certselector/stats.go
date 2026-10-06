package certselector

// ExaminedCount returns the number of candidate certificates inspected by the
// most recent Select call. It is intended for tests and benchmarks that verify
// selection cost does not grow with the total number of certificates.
func (s *Selector) ExaminedCount() int {
	return int(s.lastExaminedCount.Load())
}
