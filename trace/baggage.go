package trace

import (
	"slices"
	"strings"
)

// encodable reports whether a baggage key or value can survive the
// wire format: it must not contain the item or key/value separators.
func encodable(s string) bool {
	return !strings.ContainsAny(s, ";=")
}

// SetBaggage sets a baggage key-value pair on this span only; derived
// children and the parent are unaffected. Setting an existing key
// overwrites its value. Calls with an empty key, or with a key or
// value containing ';' or '=', are ignored because they cannot be
// represented in the wire format.
func (s *Span) SetBaggage(k, v string) {
	if k == "" || !encodable(k) || !encodable(v) {
		return
	}
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

// BaggageKeys returns the baggage keys in ascending sorted order,
// independent of insertion order. Each key appears exactly once.
func (s *Span) BaggageKeys() []string {
	s.mu.RLock()
	keys := make([]string, 0, len(s.baggage))
	for k := range s.baggage {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	slices.Sort(keys)
	return keys
}
