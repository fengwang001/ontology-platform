// Package col implements columnar slot storage: three slot-aligned int64
// columns, a liveness bit per slot, and a rowID -> slot map.
package col

import (
	"errors"
	"sync"
)

var (
	ErrNotFound = errors.New("col: rowID never allocated")
	ErrDeleted  = errors.New("col: rowID is deleted")
	ErrBadCol   = errors.New("col: bad column index")
)

// Store is the columnar storage. The zero value is ready to use.
type Store struct {
	mu            sync.RWMutex
	a, b, c       []int64 // aligned by slot: row at slot i is (a[i], b[i], c[i])
	alive         []bool  // per-slot liveness bit
	slotOf        []int   // rowID -> slot, -1 when deleted
	lastGetChecks int     // slots inspected by the most recent Get (unexported)
}

// cols returns the three column slices indexed by column number.
func (s *Store) cols() [3][]int64 { return [3][]int64{s.a, s.b, s.c} }

// Insert appends rowID (the next unallocated id) at a new tail slot.
func (s *Store) Insert(rowID int, a, b, c int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rowID != len(s.slotOf) {
		return -1
	}
	slot := len(s.a)
	s.a = append(s.a, a)
	s.b = append(s.b, b)
	s.c = append(s.c, c)
	s.alive = append(s.alive, true)
	s.slotOf = append(s.slotOf, slot)
	return slot
}

// slot resolves a live rowID to its slot. Caller must hold the lock.
func (s *Store) slot(rowID int) (int, error) {
	if rowID < 0 || rowID >= len(s.slotOf) {
		return -1, ErrNotFound
	}
	if s.slotOf[rowID] < 0 {
		return -1, ErrDeleted
	}
	return s.slotOf[rowID], nil
}

// Get returns the row's three values; all validation precedes any write.
func (s *Store) Get(rowID int) (a, b, c int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGetChecks = 0
	if rowID >= 0 && rowID < len(s.slotOf) {
		s.lastGetChecks = 1 // direct slotOf indexing: one slot inspected
	}
	slot, err := s.slot(rowID)
	if err != nil {
		return 0, 0, 0, err
	}
	return s.a[slot], s.b[slot], s.c[slot], nil
}

// Update sets one column (colIdx 0=A, 1=B, 2=C) of a live row.
func (s *Store) Update(rowID, colIdx int, v int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if colIdx < 0 || colIdx > 2 {
		return ErrBadCol
	}
	slot, err := s.slot(rowID)
	if err != nil {
		return err
	}
	s.cols()[colIdx][slot] = v
	return nil
}

// Delete tombstones a live row: the slot dies, column values stay put.
func (s *Store) Delete(rowID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot, err := s.slot(rowID)
	if err != nil {
		return err
	}
	s.alive[slot] = false
	s.slotOf[rowID] = -1
	return nil
}

// Compact drops dead slots, keeping live slots in their original order
// and every live rowID's binding; dead rowIDs stay mapped to -1.
func (s *Store) Compact() {
	s.mu.Lock()
	defer s.mu.Unlock()
	na, nb, nc := make([]int64, 0, len(s.a)), make([]int64, 0, len(s.b)), make([]int64, 0, len(s.c))
	nalive := make([]bool, 0, len(s.alive))
	nslot := make([]int, len(s.slotOf))
	for i := range nslot {
		nslot[i] = -1
	}
	for id, sl := range s.slotOf { // live ids iterate in slot order
		if sl < 0 {
			continue
		} // tombstone
		nslot[id] = len(na)
		na = append(na, s.a[sl])
		nb = append(nb, s.b[sl])
		nc = append(nc, s.c[sl])
		nalive = append(nalive, true)
	}
	s.a, s.b, s.c, s.alive, s.slotOf = na, nb, nc, nalive, nslot
}

// Snapshot copies the live rows' columns in slot order.
func (s *Store) Snapshot() [3][]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var cols [3][]int64
	for _, sl := range s.slotOf {
		if sl < 0 {
			continue
		} // tombstone
		for j, src := range s.cols() {
			cols[j] = append(cols[j], src[sl])
		}
	}
	return cols
}

// GetCostOK reports whether Get's inspected-slot count stays bounded.
func GetCostOK() bool {
	for _, m := range []int{100, 500, 2000, 10000} {
		s := &Store{}
		for i := 0; i < m; i++ {
			s.Insert(i, int64(i), 0, 0)
		}
		if _, _, _, err := s.Get(m / 2); err != nil || s.lastGetChecks > 2 {
			return false
		}
	}
	return true
}
