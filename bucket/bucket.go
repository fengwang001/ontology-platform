// Package bucket stores signature -> vector-ID postings across many tables.
package bucket

// Multi holds several parallel bucket tables, one per hash family.
type Multi struct {
	tables []map[uint64][]int
}

// NewMulti creates n empty tables.
func NewMulti(n int) *Multi {
	t := make([]map[uint64][]int, n)
	for i := range t {
		t[i] = make(map[uint64][]int)
	}
	return &Multi{tables: t}
}

// Tables returns the number of tables.
func (m *Multi) Tables() int { return len(m.tables) }

// Add appends id to the bucket sigs[i] of every table i.
func (m *Multi) Add(sigs []uint64, id int) {
	for i, s := range sigs {
		m.tables[i][s] = append(m.tables[i][s], id)
	}
}

// Collect returns the deduplicated union of postings for sigs across tables.
func (m *Multi) Collect(sigs []uint64) []int {
	seen := make(map[int]struct{})
	var out []int
	for i, s := range sigs {
		for _, id := range m.tables[i][s] {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}

// Dump exposes the raw tables for persistence. Callers must not mutate the
// result and must synchronize against concurrent Add themselves.
func (m *Multi) Dump() []map[uint64][]int { return m.tables }

// Load replaces the tables (used by persist when reading an index back).
func Load(tables []map[uint64][]int) *Multi { return &Multi{tables: tables} }

// Buckets returns the total number of non-empty buckets across tables.
func (m *Multi) Buckets() int {
	n := 0
	for _, t := range m.tables {
		n += len(t)
	}
	return n
}
