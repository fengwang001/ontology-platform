package topk

// Snapshot returns the current top elements in ranking order.
// The returned slice is a copy: mutating it does not affect the selector,
// and later Pushes do not affect it. When fewer than K elements have been
// admitted, all of them are returned.
func (s *Selector) Snapshot() []Element {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Element, len(s.items))
	copy(out, s.items)
	return out
}

// Size returns the number of elements currently held inside the selector.
// It never exceeds the capacity K.
func (s *Selector) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// Skipped returns how many pushed elements were rejected because their
// score was NaN.
func (s *Selector) Skipped() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.skipped
}

// Capacity returns the fixed capacity K the selector was built with.
func (s *Selector) Capacity() int {
	return s.k
}
