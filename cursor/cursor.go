package cursor

import (
	"math"
	"sync"
)

// maxRows is the inclusive upper bound on the number of rows a result set
// may contain (2^40).
const maxRows int64 = 1 << 40

// Error describes why a cursor operation was rejected. All errors returned
// by this package are Error values so callers can distinguish the cause.
type Error struct {
	Kind string
	msg  string
}

func (e *Error) Error() string { return e.msg }

// Sentinel errors, ordered to match the validation order in the spec.
var (
	errEmptyName        = &Error{Kind: "empty_name", msg: "cursor: name must not be empty"}
	errNegativeRows     = &Error{Kind: "negative_rows", msg: "cursor: row count must not be negative"}
	errRowsTooLarge     = &Error{Kind: "rows_too_large", msg: "cursor: row count must not exceed 2^40"}
	errNameExists       = &Error{Kind: "name_exists", msg: "cursor: cursor with that name is already open"}
	errNotFound         = &Error{Kind: "not_found", msg: "cursor: no open cursor with that name"}
	errInvalidOp        = &Error{Kind: "invalid_op", msg: "cursor: unknown fetch operation"}
	errForwardOnly      = &Error{Kind: "forward_only", msg: "cursor: operation not allowed on a forward-only cursor"}
	errNonPositiveCount = &Error{Kind: "non_positive_count", msg: "cursor: FORWARD/BACKWARD count must be positive"}
)

// Op identifies a fetch operation.
type Op string

// Supported fetch operations.
const (
	OpNext     Op = "NEXT"
	OpPrior    Op = "PRIOR"
	OpFirst    Op = "FIRST"
	OpLast     Op = "LAST"
	OpAbsolute Op = "ABSOLUTE"
	OpRelative Op = "RELATIVE"
	OpForward  Op = "FORWARD"
	OpBackward Op = "BACKWARD"
)

type cursor struct {
	mu     sync.Mutex
	n      int64
	pos    int64
	scroll bool
}

// Registry is a collection of named cursors. The zero value is ready to use.
// Operations on different cursors may run concurrently; operations on the
// same cursor are serialized internally.
type Registry struct {
	mu      sync.Mutex
	cursors map[string]*cursor
}

// NewRegistry returns an empty cursor registry.
func NewRegistry() *Registry {
	return &Registry{cursors: make(map[string]*cursor)}
}

// Open creates a named cursor over a result set of n rows.
func (r *Registry) Open(name string, n int64, scroll bool) error {
	if name == "" {
		return errEmptyName
	}
	if n < 0 {
		return errNegativeRows
	}
	if n > maxRows {
		return errRowsTooLarge
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cursors[name]; ok {
		return errNameExists
	}
	r.cursors[name] = &cursor{n: n, pos: 0, scroll: scroll}
	return nil
}

// Fetch positions the cursor according to op and k and returns the fetched
// row numbers. A legitimate fetch that lands before the first row or after
// the last row returns no rows but still updates the position.
func (r *Registry) Fetch(name string, op Op, k int64) ([]int64, error) {
	c, ok := r.lookup(name)
	if !ok {
		return nil, errNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	switch op {
	case OpNext, OpPrior, OpFirst, OpLast, OpAbsolute, OpRelative, OpForward, OpBackward:
	default:
		return nil, errInvalidOp
	}
	if !c.scroll {
		switch op {
		case OpNext, OpForward, OpRelative:
		default:
			return nil, errForwardOnly
		}
		if op == OpRelative && k < 0 {
			return nil, errForwardOnly
		}
	}
	if (op == OpForward || op == OpBackward) && k <= 0 {
		return nil, errNonPositiveCount
	}

	switch op {
	case OpNext, OpPrior, OpFirst, OpLast, OpAbsolute, OpRelative:
		t, underflow, overflow := singleRowTarget(c.pos, c.n, op, k)
		if underflow {
			c.pos = 0
			return nil, nil
		}
		if overflow {
			c.pos = c.n + 1
			return nil, nil
		}
		if t < 1 {
			c.pos = 0
			return nil, nil
		}
		if t > c.n {
			c.pos = c.n + 1
			return nil, nil
		}
		c.pos = t
		return []int64{t}, nil
	case OpForward:
		// Scanning starts at row p+1.
		start, ok := addInt64(c.pos, 1)
		if !ok {
			c.pos = c.n + 1
			return nil, nil
		}
		return c.forward(start, k), nil
	case OpBackward:
		// Scanning starts at row p-1.
		start, ok := subInt64(c.pos, 1)
		if !ok {
			c.pos = 0
			return nil, nil
		}
		return c.backward(start, k), nil
	}
	return nil, nil
}

// Position returns the current cursor position: 0 before the first row,
// n+1 after the last row.
func (r *Registry) Position(name string) (int64, error) {
	c, ok := r.lookup(name)
	if !ok {
		return 0, errNotFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pos, nil
}

// Close closes the cursor and releases its name. The name may be reopened.
func (r *Registry) Close(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cursors[name]; !ok {
		return errNotFound
	}
	delete(r.cursors, name)
	return nil
}

func (r *Registry) lookup(name string) (*cursor, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cursors[name]
	return c, ok
}

// forward returns up to k rows in ascending order beginning at row start,
// never passing row n.
func (c *cursor) forward(start, k int64) []int64 {
	if start < 1 || start > c.n {
		c.pos = c.n + 1
		return nil
	}
	available := c.n - start + 1
	if available < k {
		rows := make([]int64, 0, available)
		for row := start; row <= c.n; row++ {
			rows = append(rows, row)
		}
		c.pos = c.n + 1
		return rows
	}
	last := start + k - 1
	rows := make([]int64, 0, k)
	for row := start; row <= last; row++ {
		rows = append(rows, row)
	}
	c.pos = last
	return rows
}

// backward returns up to k rows in descending order beginning at row start,
// never passing row 1.
func (c *cursor) backward(start, k int64) []int64 {
	if start < 1 || start > c.n {
		c.pos = 0
		return nil
	}
	available := start
	if available < k {
		rows := make([]int64, 0, available)
		for row := start; row >= 1; row-- {
			rows = append(rows, row)
		}
		c.pos = 0
		return rows
	}
	last := start - k + 1
	rows := make([]int64, 0, k)
	for row := start; row >= last; row-- {
		rows = append(rows, row)
	}
	c.pos = last
	return rows
}

// singleRowTarget computes the raw target position for a single-row
// operation. The booleans report overflow-driven edge results: underflow
// means the target fell below 1, overflow means it rose above n. All
// arithmetic is checked so int64 extremes never wrap around.
func singleRowTarget(p, n int64, op Op, k int64) (t int64, underflow, overflow bool) {
	switch op {
	case OpNext:
		t, ok := addInt64(p, 1)
		if !ok {
			return 0, false, true
		}
		return t, false, false
	case OpPrior:
		t, ok := subInt64(p, 1)
		if !ok {
			return 0, true, false
		}
		return t, false, false
	case OpFirst:
		return 1, false, false
	case OpLast:
		return n, false, false
	case OpAbsolute:
		switch {
		case k > 0:
			return k, false, false
		case k < 0:
			t, ok := addInt64(n+1, k)
			if !ok {
				return 0, true, false
			}
			return t, false, false
		default:
			return 0, false, false
		}
	case OpRelative:
		t, ok := addInt64(p, k)
		if !ok {
			if k > 0 {
				return 0, false, true
			}
			return 0, true, false
		}
		return t, false, false
	}
	return 0, false, false
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func subInt64(a, b int64) (int64, bool) {
	if b > 0 && a < math.MinInt64+b {
		return 0, false
	}
	if b < 0 && a > math.MaxInt64+b {
		return 0, false
	}
	return a - b, true
}
