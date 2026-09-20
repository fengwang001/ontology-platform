package trace

import "sort"

// SetBaggage sets a baggage key on this span, overwriting any
// previous value for the same key. Baggage set on a span is visible
// to spans derived from it afterwards, but never propagates back to
// the parent or to already-derived children.
func (s *Span) SetBaggage(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.baggage[k] = v
}

// Baggage returns the value for k and whether it is present.
func (s *Span) Baggage(k string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.baggage[k]
	return v, ok
}

// BaggageKeys returns the baggage keys sorted in ascending order.
// The result is independent of insertion order, and a key set
// multiple times appears exactly once.
func (s *Span) BaggageKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.baggage)
}

// sortedKeys returns the keys of m in ascending order. Callers must
// hold the span's lock.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
