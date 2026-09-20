package ontology

import "fmt"

// CheckIndex verifies that every constraint index is consistent with
// the live records: each normalized key maps to exactly one live
// record, and every live record is indexed under its own key. It is
// intended for tests and diagnostics and is safe to call concurrently.
func (s *Store) CheckIndex() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, c := range s.constraints {
		want := make(map[string]string)
		for pk, props := range s.records {
			key, ok := c.key(s.norm, props)
			if !ok {
				continue
			}
			if other, dup := want[key]; dup {
				return fmt.Errorf("constraint %q: normalized key %q held by both %q and %q",
					c.Name, key, other, pk)
			}
			want[key] = pk
		}
		if len(want) != len(s.index[i]) {
			return fmt.Errorf("constraint %q: index has %d entries, want %d",
				c.Name, len(s.index[i]), len(want))
		}
		for key, pk := range want {
			got, ok := s.index[i][key]
			if !ok || got != pk {
				return fmt.Errorf("constraint %q: index[%q]=%q (present=%v), want %q",
					c.Name, key, got, ok, pk)
			}
		}
	}
	return nil
}
