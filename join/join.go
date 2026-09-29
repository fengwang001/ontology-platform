// Package join provides an incremental left outer join maintainer.
package join

import "sync"

// Side identifies which table a change applies to.
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "left"
	}
	return "right"
}

// Op identifies an insert or a delete.
type Op int

const (
	Insert Op = iota
	Delete
)

func (o Op) String() string {
	if o == Insert {
		return "insert"
	}
	return "delete"
}

// Row is a table row; ID is unique within its table, Key is the join key.
type Row struct {
	ID  string
	Key string
}

// Change is one insert/delete against one side.
type Change struct {
	Side Side
	Op   Op
	Row  Row
}

// Entry is one output record in the result log: an insert/delete of a join
// result row. Empty RightID denotes the null-padding row.
type Entry struct {
	Seq     int64
	Op      Op
	LeftID  string
	RightID string
	Key     string
	Reason  string
}

// ViewRow is one row of the current join result view.
type ViewRow struct {
	LeftID  string
	RightID string // empty means null-padding row
	Key     string
}

// Joiner incrementally maintains a left outer join result.
// It is safe for concurrent reads.
type Joiner struct {
	mu      sync.RWMutex
	maxRows int

	left       map[string]Row
	right      map[string]Row
	rightByKey map[string]map[string]struct{}
	leftByKey  map[string]map[string]struct{}
	matched    map[string]bool // left row ID -> has matching right rows now

	seq int64
	log []Entry
}

// New creates a Joiner. maxCaps total rows across both tables (<=0: unlimited).
func New(maxRows int) *Joiner {
	return &Joiner{
		maxRows:    maxRows,
		left:       make(map[string]Row),
		right:      make(map[string]Row),
		rightByKey: make(map[string]map[string]struct{}),
		leftByKey:  make(map[string]map[string]struct{}),
		matched:    make(map[string]bool),
	}
}
