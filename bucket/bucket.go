// Package bucket stores signature-to-ID tables and serves candidate unions.
package bucket

import "sync"

// Index holds L parallel tables mapping signatures to vector IDs.
// A single Add is atomic across all tables: concurrent readers see it
// either fully or not at all.
type Index struct {
	mu     sync.RWMutex
	tables []map[uint64][]int
}

// New creates an index with the given number of tables.
func New(tables int) *Index {
	t := make([]map[uint64][]int, tables)
	for i := range t {
		t[i] = make(map[uint64][]int)
	}
	return &Index{tables: t}
}

// NewFrom wraps already-built tables (used by persist on load).
func NewFrom(tables []map[uint64][]int) *Index {
	return &Index{tables: tables}
}

// Tables returns the number of tables.
func (ix *Index) Tables() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.tables)
}

// Add inserts id under sigs[t] in every table t.
func (ix *Index) Add(id int, sigs []uint64) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for t, sig := range sigs {
		if t < len(ix.tables) {
			ix.tables[t][sig] = append(ix.tables[t][sig], id)
		}
	}
}

// Candidates returns the deduplicated union of IDs matching sigs.
func (ix *Index) Candidates(sigs []uint64) []int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	seen := make(map[int]struct{})
	out := []int{}
	for t, sig := range sigs {
		if t >= len(ix.tables) {
			break
		}
		for _, id := range ix.tables[t][sig] {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}

// Snapshot deep-copies the tables for persistence.
func (ix *Index) Snapshot() []map[uint64][]int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]map[uint64][]int, len(ix.tables))
	for t, tbl := range ix.tables {
		cp := make(map[uint64][]int, len(tbl))
		for sig, ids := range tbl {
			cp[sig] = append([]int(nil), ids...)
		}
		out[t] = cp
	}
	return out
}
