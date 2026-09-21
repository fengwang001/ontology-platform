package ontology

import "sort"

// GroupSnapshot is an immutable view of one group's current Top-N.
type GroupSnapshot struct {
	// Group identifies the group (missing / null / value keys included).
	Group GroupKey
	// Rows holds the retained rows, best first. The slice is a copy;
	// mutating it does not affect the Selector. The row maps themselves
	// are shared and must not be mutated by callers.
	Rows []map[string]any
	// SkippedNonNumeric counts rows excluded for a missing/non-numeric score.
	SkippedNonNumeric int
	// SkippedNaN counts rows excluded for a NaN score.
	SkippedNaN int
}

// Snapshot returns all groups, ordered by group key (Missing < Null <
// Value, then by string form ascending), each with its rows in ranking
// order. Repeated calls return element-wise identical results and never
// observe a partially applied Add.
func (s *Selector) Snapshot() []GroupSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	groups := make([]*group, 0, len(s.groups))
	for _, g := range s.groups {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groupLess(groups[i].key, groups[j].key)
	})

	out := make([]GroupSnapshot, len(groups))
	for i, g := range groups {
		rows := make([]map[string]any, len(g.buf))
		for j, e := range g.buf {
			rows[j] = e.row
		}
		out[i] = GroupSnapshot{
			Group:             g.key,
			Rows:              rows,
			SkippedNonNumeric: g.skipNonNum,
			SkippedNaN:        g.skipNaN,
		}
	}
	return out
}
