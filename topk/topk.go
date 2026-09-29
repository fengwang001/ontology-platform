// Package topk provides an approximate heavy-hitters structure: a multi-row
// counting sketch (Count-Min Sketch) combined with a Space-Saving style
// candidate list. Estimates never undercount, and candidate ranking is a
// stable, reproducible total order.
package topk

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Distinguishable rejection categories, all matchable with errors.Is.
var (
	// ErrInvalidParam reports invalid constructor parameters
	// (rows, columns, candidate capacity or element bound).
	ErrInvalidParam = errors.New("topk: invalid parameter")
	// ErrElementOutOfRange reports an element outside [0, maxElement).
	ErrElementOutOfRange = errors.New("topk: element out of range")
	// ErrNonPositiveCount reports a zero arrival count.
	ErrNonPositiveCount = errors.New("topk: non-positive count")
	// ErrCountOverflow reports that accumulation would overflow a cell.
	ErrCountOverflow = errors.New("topk: count overflow")
)

// Candidate is one record in the candidate list.
type Candidate struct {
	Element  uint64
	Estimate uint64
}

// Sketch combines a multi-row counting sketch with a bounded candidate
// list. It is safe for concurrent use.
type Sketch struct {
	mu       sync.RWMutex
	rows     int
	cols     int
	capacity int
	maxElem  uint64
	seeds    []uint64
	cells    [][]uint64
	cands    []Candidate
}

// New builds a sketch with the given geometry. Elements must lie in
// [0, maxElement) and the candidate list keeps at most capacity entries.
func New(rows, cols, capacity int, maxElement uint64) (*Sketch, error) {
	if rows <= 0 {
		return nil, fmt.Errorf("%w: rows must be positive, got %d", ErrInvalidParam, rows)
	}
	if cols <= 0 {
		return nil, fmt.Errorf("%w: cols must be positive, got %d", ErrInvalidParam, cols)
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: capacity must be positive, got %d", ErrInvalidParam, capacity)
	}
	if maxElement == 0 {
		return nil, fmt.Errorf("%w: maxElement must be positive", ErrInvalidParam)
	}
	s := &Sketch{
		rows:     rows,
		cols:     cols,
		capacity: capacity,
		maxElem:  maxElement,
		seeds:    make([]uint64, rows),
		cells:    make([][]uint64, rows),
	}
	for r := 0; r < rows; r++ {
		// Deterministic per-row seeds keep hashing reproducible across runs.
		s.seeds[r] = mix64(uint64(r) + 0x9e3779b97f4a7c15)
		s.cells[r] = make([]uint64, cols)
	}
	return s, nil
}

// Add submits one arrival. It either applies fully or is rejected without
// leaving any trace on the sketch or the candidate list.
func (s *Sketch) Add(element, count uint64) error {
	if element >= s.maxElem {
		return fmt.Errorf("%w: element %d not in [0, %d)", ErrElementOutOfRange, element, s.maxElem)
	}
	if count == 0 {
		return fmt.Errorf("%w: count must be positive", ErrNonPositiveCount)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Validate every row before mutating any cell so a rejection is atomic.
	for r := 0; r < s.rows; r++ {
		c := s.locate(r, element)
		if s.cells[r][c] > math.MaxUint64-count {
			return fmt.Errorf("%w: row %d cell %d", ErrCountOverflow, r, c)
		}
	}
	for r := 0; r < s.rows; r++ {
		s.cells[r][s.locate(r, element)] += count
	}
	s.promote(element, s.estimateLocked(element))
	return nil
}

// Estimate returns the estimated count of element, never below its true
// accumulated count.
func (s *Sketch) Estimate(element uint64) (uint64, error) {
	if element >= s.maxElem {
		return 0, fmt.Errorf("%w: element %d not in [0, %d)", ErrElementOutOfRange, element, s.maxElem)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.estimateLocked(element), nil
}

// Candidates returns a snapshot ordered by estimate descending, ties broken
// by element value ascending.
func (s *Sketch) Candidates() []Candidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Candidate, len(s.cands))
	copy(out, s.cands)
	return out
}

// SelfCheck verifies internal invariants and returns nil when consistent.
func (s *Sketch) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cands) > s.capacity {
		return fmt.Errorf("topk: self-check: %d candidates exceed capacity %d", len(s.cands), s.capacity)
	}
	for i, c := range s.cands {
		if c.Element >= s.maxElem {
			return fmt.Errorf("topk: self-check: candidate %d element %d out of range", i, c.Element)
		}
		if i > 0 && !less(s.cands[i-1], c) {
			return fmt.Errorf("topk: self-check: candidates not in total order at %d", i)
		}
		for j := i + 1; j < len(s.cands); j++ {
			if s.cands[j].Element == c.Element {
				return fmt.Errorf("topk: self-check: duplicate candidate element %d", c.Element)
			}
		}
		// A recorded estimate is a past sketch estimate; cells only grow,
		// so it can never exceed the current sketch estimate.
		if cur := s.estimateLocked(c.Element); c.Estimate > cur {
			return fmt.Errorf("topk: self-check: candidate %d estimate %d above sketch estimate %d", c.Element, c.Estimate, cur)
		}
	}
	return nil
}

// mix64 is a splitmix64-style finalizer giving deterministic hashes.
func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// locate maps element to its cell column in the given row.
func (s *Sketch) locate(row int, element uint64) int {
	return int(mix64(element+s.seeds[row]) % uint64(s.cols))
}

// estimateLocked returns the minimum cell value across rows.
// Callers must hold at least the read lock.
func (s *Sketch) estimateLocked(element uint64) uint64 {
	est := s.cells[0][s.locate(0, element)]
	for r := 1; r < s.rows; r++ {
		if v := s.cells[r][s.locate(r, element)]; v < est {
			est = v
		}
	}
	return est
}

// less reports whether candidate a ranks strictly ahead of b in the total
// order: estimate descending, then element ascending.
func less(a, b Candidate) bool {
	if a.Estimate != b.Estimate {
		return a.Estimate > b.Estimate
	}
	return a.Element < b.Element
}

// promote records the fresh estimate of an arriving element: it refreshes
// the record when the element is already a candidate, otherwise inserts it
// while the list is not full or replaces the tail when it ranks higher.
// Candidates not arriving keep their recorded estimates untouched.
func (s *Sketch) promote(element, estimate uint64) {
	for i := range s.cands {
		if s.cands[i].Element == element {
			s.cands[i].Estimate = estimate
			s.sortCandidates()
			return
		}
	}
	cand := Candidate{Element: element, Estimate: estimate}
	if len(s.cands) < s.capacity {
		s.cands = append(s.cands, cand)
		s.sortCandidates()
		return
	}
	if less(cand, s.cands[len(s.cands)-1]) {
		s.cands[len(s.cands)-1] = cand
		s.sortCandidates()
	}
}

func (s *Sketch) sortCandidates() {
	sort.Slice(s.cands, func(i, j int) bool { return less(s.cands[i], s.cands[j]) })
}
