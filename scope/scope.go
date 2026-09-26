// Package scope implements the nested-scope symbol stack (depends only on sym).
package scope

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/sym"
)

// The three failure modes are distinct sentinel errors.
var (
	ErrDuplicate  = errors.New("scope: duplicate declaration in current scope")
	ErrUndeclared = errors.New("scope: undeclared name")
	ErrExitGlobal = errors.New("scope: cannot exit the global scope")
)

// Stack is a stack of lexical scopes; index 0 is the global scope.
type Stack struct {
	mu     sync.RWMutex
	scopes []*sym.Scope
	// probes counts bindings examined in lookups/dup-checks; it is
	// cumulative, unexported, and never part of the public interface.
	probes int64
}

// New returns a stack containing only the global scope.
func New() *Stack { return &Stack{scopes: []*sym.Scope{sym.New()}} }

// Enter pushes a fresh empty scope.
func (s *Stack) Enter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = append(s.scopes, sym.New())
}

// Exit pops the current scope; popping the global scope is rejected.
func (s *Stack) Exit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.scopes) == 1 {
		return ErrExitGlobal
	}
	s.scopes = s.scopes[:len(s.scopes)-1]
	return nil
}

// Declare binds name to t in the current scope only; a same-scope
// collision is rejected and leaves the first binding untouched.
func (s *Stack) Declare(name string, t sym.T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.scopes[len(s.scopes)-1]
	atomic.AddInt64(&s.probes, 1) // one binding probed for the dup check
	if _, ok := cur.Get(name); ok {
		return ErrDuplicate
	}
	cur.Put(name, t)
	return nil
}

// Lookup returns the nearest binding of name from the inside out.
func (s *Stack) Lookup(name string) (sym.T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.scopes) - 1; i >= 0; i-- {
		atomic.AddInt64(&s.probes, 1) // one binding examined in this layer
		if t, ok := s.scopes[i].Get(name); ok {
			return t, nil
		}
	}
	return 0, ErrUndeclared
}

// Depth reports the number of scopes on the stack (global included).
func (s *Stack) Depth() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.scopes)
}

// Snapshot copies the stack bottom-to-top as name->type maps.
func (s *Stack) Snapshot() []map[string]sym.T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]sym.T, len(s.scopes))
	for i, sc := range s.scopes {
		out[i] = sc.Copy()
	}
	return out
}

// probeDelta runs f (no concurrent activity) and reports how many
// bindings it examined. Unexported; package tests use it.
func (s *Stack) probeDelta(f func()) int64 {
	before := atomic.LoadInt64(&s.probes)
	f()
	return atomic.LoadInt64(&s.probes) - before
}

// SelfCheck runs a built-in operation sequence on internal stacks and
// verifies the four invariants and the constant-probe guarantee. The
// receiver is never mutated; only pass (nil) or fail (error) is seen.
func (s *Stack) SelfCheck() error {
	fail := func(f string, a ...any) error { return fmt.Errorf("selfcheck: "+f, a...) }
	c := New()
	_ = c.Declare("q", sym.Int)
	if d := c.probeDelta(func() {
		if e := c.Declare("q", sym.Bool); !errors.Is(e, ErrDuplicate) {
			panic("selfcheck: expected ErrDuplicate")
		}
	}); d != 1 {
		return fail("dup check examined %d bindings, want 1", d)
	}
	if v, e := c.Lookup("q"); e != nil || v != sym.Int {
		return fail("duplicate changed binding to %v/%v", v, e)
	}
	c.Enter()
	_ = c.Declare("q", sym.Bool) // fresh inner scope: cannot collide
	if v, _ := c.Lookup("q"); v != sym.Bool {
		return fail("inner lookup = %v, want bool", v)
	}
	_ = c.Exit()
	if v, _ := c.Lookup("q"); v != sym.Int {
		return fail("after exit lookup = %v, want int", v)
	}
	if _, e := c.Lookup("missing"); !errors.Is(e, ErrUndeclared) {
		return fail("missing = %v, want ErrUndeclared", e)
	}
	if e := New().Exit(); !errors.Is(e, ErrExitGlobal) {
		return fail("exit global = %v, want ErrExitGlobal", e)
	}
	for _, m := range []int{100, 1000, 10000} {
		f := New()
		for i := 0; i < m; i++ {
			_ = f.Declare(fmt.Sprintf("n%d", i), sym.Int)
		}
		d := f.probeDelta(func() {
			if v, e := f.Lookup(fmt.Sprintf("n%d", m-1)); e != nil || v != sym.Int {
				panic("selfcheck: O(1) lookup failed")
			}
		})
		if d != 1 {
			return fail("lookup examined %d bindings at m=%d, want 1", d, m)
		}
	}
	return nil
}
