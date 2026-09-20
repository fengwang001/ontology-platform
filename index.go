package ontology

import "sort"

// Query returns the sorted IDs whose attribute attr equals value. A nil
// value never matches anything: nil and missing attributes are excluded
// from equality semantics (use IsNull for those). The returned slice is a
// copy and shares no state with the index.
func (s *Store) Query(attr string, value any) []string {
	if value == nil {
		return []string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx, ok := s.indexes[attr]
	if !ok {
		return []string{}
	}
	return sortedKeys(idx[keyOf(value)])
}

// IsNull returns, for attribute attr, the sorted IDs where the attribute is
// missing entirely and the sorted IDs where it is present but nil. The two
// cases are reported separately.
func (s *Store) IsNull(attr string) (missing, nilVal []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	missing = []string{}
	nilVal = []string{}
	for id, attrs := range s.entities {
		v, ok := attrs[attr]
		switch {
		case !ok:
			missing = append(missing, id)
		case v == nil:
			nilVal = append(nilVal, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(nilVal)
	return missing, nilVal
}

// AttrCounts reports how many rows, for attribute attr, are indexed (have a
// concrete non-nil value), are missing the attribute, and hold an explicit
// nil. The three always sum to Len().
func (s *Store) AttrCounts(attr string) (indexed, missing, nilVal int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, bucket := range s.indexes[attr] {
		indexed += len(bucket)
	}
	for _, attrs := range s.entities {
		v, ok := attrs[attr]
		switch {
		case !ok:
			missing++
		case v == nil:
			nilVal++
		}
	}
	return indexed, missing, nilVal
}

// DistinctValues reports how many distinct values the index on attr holds.
func (s *Store) DistinctValues(attr string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.indexes[attr])
}

// TotalEntries reports the total number of (value, ID) entries across all
// indexes. After any sequence of mutations it equals the number of rows
// that currently carry a concrete value on an indexed attribute.
func (s *Store) TotalEntries() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, idx := range s.indexes {
		for _, bucket := range idx {
			total += len(bucket)
		}
	}
	return total
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
