package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// Verify recomputes every equality index from scratch via a full table scan
// and compares it against the live index structures. It returns nil when
// they agree exactly, or an error locating the first discrepancy: which
// attribute, which value, and which IDs are missing from or stale in the
// index. It must pass after any sequence of Upsert/Delete operations.
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	expected := make(map[string]map[string]map[string]struct{}, len(s.indexed))
	for attr := range s.indexed {
		expected[attr] = make(map[string]map[string]struct{})
	}
	for id, attrs := range s.entities {
		for attr := range s.indexed {
			v, ok := attrs[attr]
			if !ok || v == nil {
				continue
			}
			key := keyOf(v)
			bucket := expected[attr][key]
			if bucket == nil {
				bucket = make(map[string]struct{})
				expected[attr][key] = bucket
			}
			bucket[id] = struct{}{}
		}
	}

	attrs := make([]string, 0, len(s.indexed))
	for attr := range s.indexed {
		attrs = append(attrs, attr)
	}
	sort.Strings(attrs)
	for _, attr := range attrs {
		if err := diffIndex(attr, expected[attr], s.indexes[attr]); err != nil {
			return err
		}
	}
	return nil
}

func diffIndex(attr string, want, got map[string]map[string]struct{}) error {
	keys := make(map[string]struct{}, len(want)+len(got))
	for k := range want {
		keys[k] = struct{}{}
	}
	for k := range got {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, key := range sorted {
		missing := setDiff(want[key], got[key]) // in scan, not in index
		stale := setDiff(got[key], want[key])   // in index, not in scan
		if len(missing) > 0 || len(stale) > 0 {
			return fmt.Errorf("verify: attr %q value %s: index missing IDs [%s], stale IDs [%s]",
				attr, key, strings.Join(missing, ","), strings.Join(stale, ","))
		}
	}
	return nil
}

func setDiff(a, b map[string]struct{}) []string {
	out := []string{}
	for id := range a {
		if _, ok := b[id]; !ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
