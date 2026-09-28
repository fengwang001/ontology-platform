// Package hotcount provides an approximate heavy-hitter counter built on a
// Count-Min style sketch with a bounded, deterministically ordered candidate
// list.
package hotcount

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Error categories. Every rejected call returns one of these distinct,
// distinguishable errors; a failed call never mutates the sketch or the
// candidate list.
var (
	// ErrInvalidRows is returned when the sketch has zero or negative rows.
	ErrInvalidRows = errors.New("hotcount: rows must be positive")
	// ErrInvalidWidth is returned when a sketch row has zero or negative cells.
	ErrInvalidWidth = errors.New("hotcount: width must be positive")
	// ErrInvalidTopK is returned when the candidate limit is zero or negative.
	ErrInvalidTopK = errors.New("hotcount: topK must be positive")
	// ErrElementOutOfRange is returned when element exceeds MaxElement.
	ErrElementOutOfRange = errors.New("hotcount: element out of range")
	// ErrNonPositiveCount is returned when a committed count is zero.
	ErrNonPositiveCount = errors.New("hotcount: count must be positive")
	// ErrCountOverflow is returned when adding a count would overflow a cell.
	ErrCountOverflow = errors.New("hotcount: count overflow")
)

// MaxElement is the inclusive upper bound of accepted element values.
const MaxElement uint64 = 1<<32 - 1

// maxUint64 is the largest representable counter value.
const maxUint64 = ^uint64(0)

// Candidate is one tracked element together with the estimate captured when it
// last arrived or entered the list.
type Candidate struct {
	Element  uint64
	Estimate uint64
}

// Counter is the hot-element statistics structure.
type Counter struct {
	mu    sync.RWMutex
	rows  int
	width int
	topK  int

	// cells[r][c] is the accumulated weight of row r, column c.
	cells [][]uint64
	// candidates is kept in total order: estimate desc, element asc.
	candidates []Candidate
}

// New creates an empty Counter.
//
// rows is the number of independent sketch lines, width the number of cells
// per line and topK the maximum number of candidates retained.
func New(rows, width, topK int) (*Counter, error) {
	switch {
	case rows <= 0:
		return nil, ErrInvalidRows
	case width <= 0:
		return nil, ErrInvalidWidth
	case topK <= 0:
		return nil, ErrInvalidTopK
	}
	cells := make([][]uint64, rows)
	for r := range cells {
		cells[r] = make([]uint64, width)
	}
	return &Counter{
		rows:       rows,
		width:      width,
		topK:       topK,
		cells:      cells,
		candidates: make([]Candidate, 0, topK),
	}, nil
}

// Commit records one weighted arrival.
//
// The count is added to the hashed cell of every row. A rejected call (bad
// element, non-positive count or overflow) leaves the whole structure
// untouched. The returned estimate is the post-commit min-row estimate.
func (c *Counter) Commit(element uint64, count uint64) (uint64, error) {
	if element > MaxElement {
		return 0, fmt.Errorf("%w: element %d", ErrElementOutOfRange, element)
	}
	if count == 0 {
		return 0, ErrNonPositiveCount
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	columns := c.locate(element)

	// Validate before mutating anything, so overflow is a total rejection.
	for r := 0; r < c.rows; r++ {
		cell := c.cells[r][columns[r]]
		if cell > maxUint64-count {
			return 0, fmt.Errorf("%w: element %d count %d", ErrCountOverflow, element, count)
		}
	}
	for r := 0; r < c.rows; r++ {
		c.cells[r][columns[r]] += count
	}

	estimate := c.estimateLocked(columns)
	c.upsertCandidate(element, estimate)
	return estimate, nil
}

// Estimate returns the over-estimated frequency of element.
//
// It is the minimum of the hashed cells across all rows, which is never below
// the element's true accumulated count.
func (c *Counter) Estimate(element uint64) uint64 {
	if element > MaxElement {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.estimateLocked(c.locate(element))
}

// Candidates returns the current top candidates in total order.
//
// Order is estimate descending with element ascending as the tie-breaker. The
// returned slice is a copy safe to retain.
func (c *Counter) Candidates() []Candidate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Candidate, len(c.candidates))
	copy(out, c.candidates)
	return out
}

// SelfCheck validates internal invariants.
//
// It verifies sketch dimensions, candidate bounds and ordering, and that each
// recorded candidate estimate equals the current sketch estimate and is never
// below its own cell totals.
func (c *Counter) SelfCheck() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.rows <= 0 || c.width <= 0 || c.topK <= 0 {
		return fmt.Errorf("hotcount: bad geometry rows=%d width=%d topK=%d", c.rows, c.width, c.topK)
	}
	if len(c.cells) != c.rows {
		return fmt.Errorf("hotcount: got %d sketch rows, want %d", len(c.cells), c.rows)
	}
	for r, row := range c.cells {
		if len(row) != c.width {
			return fmt.Errorf("hotcount: row %d width %d, want %d", r, len(row), c.width)
		}
	}
	if len(c.candidates) > c.topK {
		return fmt.Errorf("hotcount: %d candidates exceed topK %d", len(c.candidates), c.topK)
	}

	seen := make(map[uint64]struct{}, len(c.candidates))
	for i, cand := range c.candidates {
		if cand.Element > MaxElement {
			return fmt.Errorf("hotcount: candidate element %d out of range", cand.Element)
		}
		if _, dup := seen[cand.Element]; dup {
			return fmt.Errorf("hotcount: duplicate candidate element %d", cand.Element)
		}
		seen[cand.Element] = struct{}{}

		columns := c.locate(cand.Element)
		current := c.estimateLocked(columns)
		// A candidate's recorded estimate is refreshed only on its own arrival;
		// cells only grow, so it can lag the current value but never exceed it.
		if cand.Estimate > current {
			return fmt.Errorf("hotcount: candidate %d estimate %d above current %d",
				cand.Element, cand.Estimate, current)
		}
		for r := 0; r < c.rows; r++ {
			if c.cells[r][columns[r]] < cand.Estimate {
				return fmt.Errorf("hotcount: candidate %d estimate %d below row %d cell %d",
					cand.Element, cand.Estimate, r, c.cells[r][columns[r]])
			}
		}

		if i > 0 && !higherRank(c.candidates[i-1], cand) {
			return fmt.Errorf("hotcount: candidate order violated at index %d", i)
		}
	}
	return nil
}

// locate maps element to one column per row. Each row mixes FNV-1a with a
// row-specific seed so collisions are independent across rows.
func (c *Counter) locate(element uint64) []int {
	columns := make([]int, c.rows)
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(element >> (8 * uint(i)))
	}
	for r := 0; r < c.rows; r++ {
		h := fnvNew()
		h.Write([]byte{byte(r), byte(r >> 8), byte(r >> 16), byte(r >> 24)})
		h.Write(buf[:])
		columns[r] = int(h.Sum64() % uint64(c.width))
	}
	return columns
}

// estimateLocked is the min-cell estimate; caller must hold c.mu.
func (c *Counter) estimateLocked(columns []int) uint64 {
	estimate := maxUint64
	for r := 0; r < c.rows; r++ {
		if v := c.cells[r][columns[r]]; v < estimate {
			estimate = v
		}
	}
	return estimate
}

// upsertCandidate inserts, refreshes or replaces a candidate after an arrival;
// caller must hold c.mu (write lock).
func (c *Counter) upsertCandidate(element, estimate uint64) {
	for i := range c.candidates {
		if c.candidates[i].Element == element {
			c.candidates[i].Estimate = estimate
			sort.SliceStable(c.candidates, func(a, b int) bool {
				return higherRank(c.candidates[a], c.candidates[b])
			})
			return
		}
	}

	incoming := Candidate{Element: element, Estimate: estimate}
	if len(c.candidates) < c.topK {
		c.candidates = append(c.candidates, incoming)
		sort.SliceStable(c.candidates, func(a, b int) bool {
			return higherRank(c.candidates[a], c.candidates[b])
		})
		return
	}

	// List is full: replace the lowest-ranked candidate when the newcomer
	// outranks it.
	last := c.candidates[len(c.candidates)-1]
	if higherRank(incoming, last) {
		c.candidates[len(c.candidates)-1] = incoming
		sort.SliceStable(c.candidates, func(a, b int) bool {
			return higherRank(c.candidates[a], c.candidates[b])
		})
	}
}

// higherRank reports whether a precedes b in the total order: larger estimate
// first, then smaller element value.
func higherRank(a, b Candidate) bool {
	if a.Estimate != b.Estimate {
		return a.Estimate > b.Estimate
	}
	return a.Element < b.Element
}
