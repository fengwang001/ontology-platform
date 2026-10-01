// Package unwind implements an exception-table stack unwinder.
//
// Functions are registered with an exception table whose entries are
// (start, end, handler, type) tuples. Ranges are half-open [start, end)
// and type 0 matches any exception type. When an exception is thrown the
// unwinder walks the call stack from the top frame downward, looking up
// each frame's function table in declaration order, and resumes the first
// matching frame at the entry's handler address.
package unwind

import (
	"errors"
	"fmt"
	"sync"
)

// Entry is a single exception-table row: [Start, End) guarded by Handler
// for exception Type. Type 0 matches any thrown type.
type Entry struct {
	Start   int
	End     int
	Handler int
	Type    int
}

// Frame is one call-stack frame: a function name and a pc. The top frame's
// pc is the currently executing instruction; every other frame's pc is the
// return address (the instruction after the call).
type Frame struct {
	Func string
	PC   int
}

// Distinguishable rejection reasons, matchable with errors.Is.
var (
	ErrFunctionExists   = errors.New("unwind: function already registered")
	ErrInvalidRange     = errors.New("unwind: entry start must be >= end")
	ErrHandlerInRange   = errors.New("unwind: handler lies within its own range")
	ErrNegativeType     = errors.New("unwind: entry type is negative")
	ErrFunctionNotFound = errors.New("unwind: function not registered")
	ErrNegativePC       = errors.New("unwind: pc is negative")
	ErrZeroReturnPC     = errors.New("unwind: non-bottom frame pc is zero")
	ErrNonPositiveType  = errors.New("unwind: exception type is not a positive integer")
	ErrEmptyStack       = errors.New("unwind: stack is empty")
	ErrUncaught         = errors.New("unwind: exception not caught")
)

// Unwinder holds registered function tables and the current frame stack.
// All methods are safe for concurrent use; results are equivalent to some
// serial execution of the calls.
type Unwinder struct {
	mu     sync.Mutex
	tables map[string][]Entry
	stack  []Frame
}

// New returns an empty Unwinder.
func New() *Unwinder {
	return &Unwinder{tables: make(map[string][]Entry)}
}

// Register adds the exception table for name. It fails if name is already
// registered, or if any entry is invalid (checked in declaration order;
// within one entry: range, then handler-in-range, then negative type).
// A rejected registration leaves the tables unchanged.
func (u *Unwinder) Register(name string, entries []Entry) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.tables[name]; ok {
		return fmt.Errorf("%w: %q", ErrFunctionExists, name)
	}
	for i, e := range entries {
		if e.Start >= e.End {
			return fmt.Errorf("%w: %q entry %d [%d,%d)", ErrInvalidRange, name, i, e.Start, e.End)
		}
		if e.Handler >= e.Start && e.Handler < e.End {
			return fmt.Errorf("%w: %q entry %d handler %d in [%d,%d)", ErrHandlerInRange, name, i, e.Handler, e.Start, e.End)
		}
		if e.Type < 0 {
			return fmt.Errorf("%w: %q entry %d type %d", ErrNegativeType, name, i, e.Type)
		}
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	u.tables[name] = cp
	return nil
}

// Push appends a frame for funcName at pc. The pc must be non-negative,
// and a non-bottom frame's pc (a return address) must be non-zero.
// A rejected push leaves the stack unchanged.
func (u *Unwinder) Push(funcName string, pc int) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.tables[funcName]; !ok {
		return fmt.Errorf("%w: %q", ErrFunctionNotFound, funcName)
	}
	if pc < 0 {
		return fmt.Errorf("%w: %q pc %d", ErrNegativePC, funcName, pc)
	}
	if len(u.stack) > 0 && pc == 0 {
		return fmt.Errorf("%w: %q", ErrZeroReturnPC, funcName)
	}
	u.stack = append(u.stack, Frame{Func: funcName, PC: pc})
	return nil
}

// Throw raises an exception of type t (a positive integer) and unwinds.
// The top frame is looked up with p = pc, deeper frames with p = pc-1
// (return address minus one, i.e. the call instruction). On a hit the
// frames above the matching frame are popped, its pc is set to the
// handler, and the frame's stack index and the handler are returned.
// If nothing matches, ErrUncaught is returned and the stack is unchanged.
func (u *Unwinder) Throw(t int) (frameIndex int, handler int, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if t <= 0 {
		return -1, -1, fmt.Errorf("%w: %d", ErrNonPositiveType, t)
	}
	if len(u.stack) == 0 {
		return -1, -1, ErrEmptyStack
	}
	for i := len(u.stack) - 1; i >= 0; i-- {
		p := u.stack[i].PC
		if i != len(u.stack)-1 {
			p--
		}
		if e, ok := lookup(u.tables[u.stack[i].Func], p, t); ok {
			u.stack = u.stack[:i+1]
			u.stack[i].PC = e.Handler
			return i, e.Handler, nil
		}
	}
	return -1, -1, ErrUncaught
}

// lookup returns the first entry in declaration order whose half-open
// range contains p and whose type is 0 or equals t.
func lookup(entries []Entry, p, t int) (Entry, bool) {
	for _, e := range entries {
		if e.Start <= p && p < e.End && (e.Type == 0 || e.Type == t) {
			return e, true
		}
	}
	return Entry{}, false
}

// Stack returns a snapshot copy of the current frame stack (bottom first).
func (u *Unwinder) Stack() []Frame {
	u.mu.Lock()
	defer u.mu.Unlock()
	cp := make([]Frame, len(u.stack))
	copy(cp, u.stack)
	return cp
}

// Entries returns a snapshot copy of the table registered for name.
func (u *Unwinder) Entries(name string) ([]Entry, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	entries, ok := u.tables[name]
	if !ok {
		return nil, false
	}
	cp := make([]Entry, len(entries))
	copy(cp, entries)
	return cp, true
}
