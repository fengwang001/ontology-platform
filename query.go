package ontology

import "sort"

// Lookup returns the IDs whose attr equals value, sorted
// lexicographically ascending. Missing attributes and nil values are
// never returned: equality with any concrete value cannot match them.
// The returned slice is freshly allocated and detached from the index;
// mutating it does not affect the store.
func (s *Store) Lookup(attr string, value any) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := keyOf(value)
	if !ok {
		return []string{}
	}
	bucket := s.index[attr][key]
	out := make([]string, 0, len(bucket))
	for id := range bucket {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// NullInfo splits the IsNull result into its two disjoint causes.
type NullInfo struct {
	// Missing holds IDs that do not have the attribute at all.
	Missing []string
	// Nil holds IDs whose attribute is present with a nil value.
	Nil []string
}

// Total returns len(Missing) + len(Nil).
func (n NullInfo) Total() int { return len(n.Missing) + len(n.Nil) }

// IsNull reports which entities are null for attr, distinguishing a
// missing attribute from a present-but-nil value. Both lists are
// sorted lexicographically and freshly allocated.
func (s *Store) IsNull(attr string) NullInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pres := s.present[attr]
	missing := make([]string, 0)
	for _, id := range s.ids {
		if _, ok := pres[id]; !ok {
			missing = append(missing, id)
		}
	}
	nils := make([]string, 0, len(s.nilSet[attr]))
	for id := range s.nilSet[attr] {
		nils = append(nils, id)
	}
	sort.Strings(missing)
	sort.Strings(nils)
	return NullInfo{Missing: missing, Nil: nils}
}

// Counts decomposes the row count for attr into its three disjoint
// parts: rows carrying an indexed (non-nil) value, rows missing the
// attribute, and rows whose value is nil. It always holds that
// indexed + missing + null == RowCount().
func (s *Store) Counts(attr string) (indexed, missing, null int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, bucket := range s.index[attr] {
		indexed += len(bucket)
	}
	null = len(s.nilSet[attr])
	missing = len(s.ids) - len(s.present[attr])
	return indexed, missing, null
}

// IndexStats returns the number of distinct values and the total
// number of (value, ID) entries across all equality indexes. After
// any sequence of mutations, total entries equals the number of rows
// carrying a non-nil value on an indexed attribute, summed over
// indexed attributes.
func (s *Store) IndexStats() (distinctValues, totalEntries int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, a := range s.attrs {
		distinctValues += len(s.index[a])
		for _, bucket := range s.index[a] {
			totalEntries += len(bucket)
		}
	}
	return distinctValues, totalEntries
}
