// Package cube maintains the materialised sums of a three-dimensional
// CUBE. It depends only on package dim. All state lives in process memory.
package cube

import (
	"errors"
	"sort"

	"ontology/dim"
)

// Sentinel errors. They are pairwise distinct and detectable with errors.Is.
var (
	ErrInvalidMaxCells = errors.New("cube: maxCells must be positive")
	ErrCellLimit       = errors.New("cube: Add would make non-empty cells exceed maxCells")
	ErrFactNotFound    = errors.New("cube: Remove targets cells that are not present")
)

// Cell is one non-empty cube cell: its key and the signed sum over it.
type Cell struct {
	Key dim.Key
	Sum int64
}

// Cube holds sums keyed by cell. The zero value is not usable; use New.
type Cube struct {
	cells    map[dim.Key]int64
	maxCells int

	// touchedCount is the number of cell keys touched by the most recent
	// Add/Remove. It is intentionally unexported: the 8-mask walk always
	// touches exactly 8 keys, independent of the number of stored cells.
	touchedCount int
}

// New creates an empty cube that may hold at most maxCells non-empty cells.
func New(maxCells int) (*Cube, error) {
	if maxCells <= 0 {
		return nil, ErrInvalidMaxCells
	}
	return &Cube{cells: map[dim.Key]int64{}, maxCells: maxCells}, nil
}

// Add adds v to each of the fact's 8 cells. If the resulting number of
// non-empty cells would exceed maxCells the whole Add is rejected and no
// state changes.
func (c *Cube) Add(a, b, cc string, v int64) error {
	keys := dim.Cells(a, b, cc)

	// Pre-check against the exact post-operation non-empty cell count. The
	// 8 keys are pairwise distinct (their ALL patterns differ).
	final := len(c.cells)
	for _, k := range keys {
		old := c.cells[k] // zero when absent; absent cells sum to 0
		nv := old + v
		if old != 0 && nv == 0 {
			final--
		} else if old == 0 && nv != 0 {
			final++
		}
	}
	if final > c.maxCells {
		c.touchedCount = 0
		return ErrCellLimit
	}

	for _, k := range keys {
		nv := c.cells[k] + v
		if nv == 0 {
			delete(c.cells, k) // zero sums are never retained
		} else {
			c.cells[k] = nv
		}
	}
	c.touchedCount = 8
	return nil
}

// Remove subtracts v from each of the fact's 8 cells. Every one of the 8
// cells must already exist, otherwise the fact is considered absent and the
// whole Remove is rejected without touching any state.
func (c *Cube) Remove(a, b, cc string, v int64) error {
	keys := dim.Cells(a, b, cc)
	for _, k := range keys {
		if _, ok := c.cells[k]; !ok {
			c.touchedCount = 0
			return ErrFactNotFound
		}
	}
	for _, k := range keys {
		nv := c.cells[k] - v
		if nv == 0 {
			delete(c.cells, k)
		} else {
			c.cells[k] = nv
		}
	}
	c.touchedCount = 8
	return nil
}

// View returns all non-empty cells sorted deterministically by key. The
// returned slice is independent of the cube's internal storage.
func (c *Cube) View() []Cell {
	out := make([]Cell, 0, len(c.cells))
	for k, s := range c.cells {
		out = append(out, Cell{Key: k, Sum: s})
	}
	sort.Slice(out, func(i, j int) bool { return lessKey(out[i].Key, out[j].Key) })
	return out
}

func lessKey(x, y dim.Key) bool {
	if x.AAll != y.AAll {
		return y.AAll // concrete (false) sorts before ALL (true)
	}
	if !x.AAll && x.AVal != y.AVal {
		return x.AVal < y.AVal
	}
	if x.BAll != y.BAll {
		return y.BAll
	}
	if !x.BAll && x.BVal != y.BVal {
		return x.BVal < y.BVal
	}
	if x.CAll != y.CAll {
		return y.CAll
	}
	if !x.CAll {
		return x.CVal < y.CVal
	}
	return false
}
