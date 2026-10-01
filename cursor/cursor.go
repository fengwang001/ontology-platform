// Package cursor implements a scrollable result-set cursor manager.
package cursor

import (
	"errors"
	"math"
	"sync"
)

// Operation identifies a Fetch positioning operation.
type Operation int

const (
	Next Operation = iota + 1
	Prior
	First
	Last
	Absolute
	Relative
	Forward
	Backward
)

// Sentinel errors, one per reject reason listed in the specification.
var (
	ErrEmptyName    = errors.New("cursor: name is empty")
	ErrInvalidN     = errors.New("cursor: row count is negative or exceeds 2^40")
	ErrNameExists   = errors.New("cursor: name already open")
	ErrNotFound     = errors.New("cursor: not found")
	ErrInvalidOp    = errors.New("cursor: unknown fetch operation")
	ErrForwardOnly  = errors.New("cursor: operation forbidden on forward-only cursor")
	ErrNonPositiveK = errors.New("cursor: FORWARD/BACKWARD requires positive k")
)

const maxRows = int64(1) << 40

// Manager owns a set of named cursors.
type Manager struct {
	mu      sync.Mutex
	cursors map[string]*cursor
}

// NewManager creates an empty cursor manager.
func NewManager() *Manager {
	return &Manager{cursors: make(map[string]*cursor)}
}

type cursor struct {
	mu     sync.Mutex
	n      int64
	scroll bool
	pos    int64 // always in [0, n+1]; 0 = before first, n+1 = after last
}

// Open creates a named cursor over a result set of n rows.
func (m *Manager) Open(name string, n int64, scroll bool) error {
	if name == "" {
		return ErrEmptyName
	}
	if n < 0 || n > maxRows {
		return ErrInvalidN
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cursors[name]; ok {
		return ErrNameExists
	}
	m.cursors[name] = &cursor{n: n, scroll: scroll, pos: 0}
	return nil
}

// Fetch applies a single-row or bulk positioning operation.
func (m *Manager) Fetch(name string, op Operation, k int64) (rows []int64, err error) {
	m.mu.Lock()
	c, ok := m.cursors[name]
	m.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	if !validOp(op) {
		return nil, ErrInvalidOp
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.scroll && !allowedForwardOnly(op, k) {
		return nil, ErrForwardOnly
	}
	if (op == Forward || op == Backward) && k <= 0 {
		return nil, ErrNonPositiveK
	}

	n := c.n
	switch op {
	case Next:
		return c.move(c.pos + 1), nil
	case Prior:
		return c.move(c.pos - 1), nil
	case First:
		return c.move(1), nil
	case Last:
		return c.move(n), nil
	case Absolute:
		var t int64
		switch {
		case k > 0:
			t = k
		case k < 0:
			t = n + 1 + k // safe: k in [MinInt64+1, -1], n <= 2^40
		default:
			t = 0
		}
		return c.move(t), nil
	case Relative:
		t, ok := addChecked(c.pos, k)
		if !ok {
			t = edgeForOverflow(c.pos, k, n)
		}
		return c.move(t), nil
	case Forward:
		start := c.pos + 1
		if start > n {
			c.pos = n + 1
			return nil, nil
		}
		available := n - start + 1
		count := k
		if count > available {
			count = available
		}
		out := make([]int64, 0, count)
		for r := start; r < start+count; r++ {
			out = append(out, r)
		}
		if k > available {
			c.pos = n + 1
		} else {
			c.pos = start + count - 1
		}
		return out, nil
	case Backward:
		start := c.pos - 1
		if start < 1 {
			c.pos = 0
			return nil, nil
		}
		available := start
		count := k
		if count > available {
			count = available
		}
		out := make([]int64, 0, count)
		for r := start; r > start-count; r-- {
			out = append(out, r)
		}
		if k > available {
			c.pos = 0
		} else {
			c.pos = start - count + 1
		}
		return out, nil
	}
	return nil, ErrInvalidOp
}

// Position reports the current cursor position.
func (m *Manager) Position(name string) (int64, error) {
	m.mu.Lock()
	c, ok := m.cursors[name]
	m.mu.Unlock()
	if !ok {
		return 0, ErrNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pos, nil
}

// Close closes a cursor and releases its name.
func (m *Manager) Close(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cursors[name]; !ok {
		return ErrNotFound
	}
	delete(m.cursors, name)
	return nil
}

func validOp(op Operation) bool {
	return op >= Next && op <= Backward
}

func allowedForwardOnly(op Operation, k int64) bool {
	switch op {
	case Next, Forward:
		return true
	case Relative:
		return k >= 0
	default:
		return false
	}
}

// move applies the single-row target-position rule and returns the fetched
// row number when the target lands on a row.
func (c *cursor) move(t int64) []int64 {
	switch {
	case t < 1:
		c.pos = 0
		return nil
	case t > c.n:
		c.pos = c.n + 1
		return nil
	default:
		c.pos = t
		return []int64{t}
	}
}

// addChecked returns p+k using exact integer semantics: ok is false when the
// true mathematical sum does not fit in int64.
func addChecked(p, k int64) (sum int64, ok bool) {
	if k > 0 && p > math.MaxInt64-k {
		return 0, false
	}
	if k < 0 && p < math.MinInt64-k {
		return 0, false
	}
	return p + k, true
}

// edgeForOverflow maps an overflowing RELATIVE move onto the legal edge:
// a positive overflow lands after the last row, a negative one before the
// first row.
func edgeForOverflow(p, k, n int64) int64 {
	if k > 0 {
		return n + 1
	}
	return 0
}
