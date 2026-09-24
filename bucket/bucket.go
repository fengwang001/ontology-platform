// Package bucket implements signature -> vector-ID bucket tables.
package bucket

import "sort"

// Table maps a b-bit signature to the list of vector IDs sharing that bucket.
type Table struct {
	buckets map[uint64][]int32
}

// NewTable creates an empty bucket table.
func NewTable() *Table { return &Table{buckets: make(map[uint64][]int32)} }

// Add places id in the bucket keyed by sig.
func (t *Table) Add(sig uint64, id int32) {
	t.buckets[sig] = append(t.buckets[sig], id)
}

// Get returns the IDs in one bucket and whether the bucket exists.
func (t *Table) Get(sig uint64) ([]int32, bool) {
	ids, ok := t.buckets[sig]
	return ids, ok
}

// BucketCount returns the number of non-empty buckets.
func (t *Table) BucketCount() int { return len(t.buckets) }

// Snapshot returns a stable deep copy of signature -> sorted IDs.
func (t *Table) Snapshot() map[uint64][]int32 {
	out := make(map[uint64][]int32, len(t.buckets))
	for sig, ids := range t.buckets {
		cp := make([]int32, len(ids))
		copy(cp, ids)
		sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
		out[sig] = cp
	}
	return out
}

// FromSnapshot rebuilds a table from persisted data.
func FromSnapshot(m map[uint64][]int32) *Table {
	t := NewTable()
	for sig, ids := range m {
		cp := make([]int32, len(ids))
		copy(cp, ids)
		t.buckets[sig] = cp
	}
	return t
}

// MultiTable holds L independent bucket tables.
type MultiTable struct {
	tables []*Table
}

// NewMultiTable creates n empty tables.
func NewMultiTable(n int) *MultiTable {
	m := &MultiTable{tables: make([]*Table, n)}
	for i := range m.tables {
		m.tables[i] = NewTable()
	}
	return m
}

// Add inserts id under sig in table t.
func (m *MultiTable) Add(t int, sig uint64, id int32) { m.tables[t].Add(sig, id) }

// Table returns one bucket table.
func (m *MultiTable) Table(t int) *Table { return m.tables[t] }

// Len returns the number of tables.
func (m *MultiTable) Len() int { return len(m.tables) }

// Candidates collects and de-duplicates IDs from the given (table, signature)
// pairs. used first tables L (of a larger nested family) for monotone recall.
func (m *MultiTable) Candidates(pairs []SigPair) []int32 {
	seen := make(map[int32]struct{})
	var out []int32
	for _, p := range pairs {
		ids, ok := m.tables[p.Table].Get(p.Sig)
		if !ok {
			continue
		}
		for _, id := range ids {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// SigPair identifies one bucket to probe: table index and its signature.
type SigPair struct {
	Table int
	Sig   uint64
}

// DropMissing removes bucket references whose ID is not in valid (the set of
// known live vector IDs). It returns the number of dropped references.
func (m *MultiTable) DropMissing(valid map[int32]bool) int {
	dropped := 0
	for _, tbl := range m.tables {
		for sig, ids := range tbl.buckets {
			kept := ids[:0]
			for _, id := range ids {
				if valid[id] {
					kept = append(kept, id)
				} else {
					dropped++
				}
			}
			if len(kept) == 0 {
				delete(tbl.buckets, sig)
			} else {
				tbl.buckets[sig] = kept
			}
		}
	}
	return dropped
}
