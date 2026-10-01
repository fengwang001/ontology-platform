package intervalindex

import (
	"sort"
	"sync"
)

// naiveIndex is the brute-force reference implementation: every query scans
// all stored intervals. It exists only to differential-test the augmented
// index.
type naiveIndex struct {
	mu   sync.Mutex
	rows []naiveRow
}

type naiveRow struct {
	id     int64
	lo, hi int64
}

func newNaive() *naiveIndex { return &naiveIndex{} }

func (m *naiveIndex) Insert(id, lo, hi int64) error {
	if id <= 0 {
		return ErrNonPositiveID
	}
	if lo >= hi {
		return ErrEmptyInterval
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.id == id {
			return ErrDuplicateID
		}
	}
	m.rows = append(m.rows, naiveRow{id, lo, hi})
	return nil
}

func (m *naiveIndex) Remove(id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.rows {
		if r.id == id {
			m.rows = append(m.rows[:i], m.rows[i+1:]...)
			return nil
		}
	}
	return ErrIDNotFound
}

func (m *naiveIndex) snapshotSorted() []naiveRow {
	rows := append([]naiveRow(nil), m.rows...)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].lo < rows[j].lo ||
			(rows[i].lo == rows[j].lo && rows[i].id < rows[j].id)
	})
	return rows
}

func (m *naiveIndex) Stab(x int64) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []int64
	for _, r := range m.snapshotSorted() {
		if r.lo <= x && x < r.hi {
			out = append(out, r.id)
		}
	}
	return out
}

func (m *naiveIndex) Overlap(a, b int64) ([]int64, error) {
	if a >= b {
		return nil, ErrInvalidQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []int64
	for _, r := range m.snapshotSorted() {
		// Half-open intersection: lo < b && a < hi.
		if r.lo < b && a < r.hi {
			out = append(out, r.id)
		}
	}
	return out, nil
}

func (m *naiveIndex) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
