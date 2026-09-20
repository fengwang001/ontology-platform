package ontology

import (
	"fmt"
	"strings"
)

// SelfCheck verifies the index invariants and returns a
// descriptive error on the first violation found:
//   - no normalized key is shared by two live records;
//   - the maintained index exactly matches the index rebuilt
//     from the live records (no stale or missing entries).
//
// It is intended to be called directly from tests, including
// after concurrent writers.
func (s *Store) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, c := range s.constraints {
		want := map[string]string{}
		for id, rec := range s.records {
			vals, ok := c.normValues(rec.Props, s.norm)
			if !ok {
				continue
			}
			key := encodeKey(vals)
			if prev, dup := want[key]; dup {
				return fmt.Errorf("constraint %q: normalized key [%s] shared by live records %q and %q",
					c.Name, strings.Join(vals, " | "), prev, id)
			}
			want[key] = id
		}
		got := s.index[c.Name]
		for key, id := range want {
			if got[key] != id {
				return fmt.Errorf("constraint %q: index missing entry %q -> record %q", c.Name, key, id)
			}
		}
		for key, id := range got {
			if _, alive := want[key]; !alive {
				return fmt.Errorf("constraint %q: stale index entry %q -> record %q", c.Name, key, id)
			}
		}
	}
	return nil
}
