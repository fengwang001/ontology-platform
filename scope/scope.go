// Package scope implements the nested-scope stack: Enter/Exit/Declare/Lookup
// with shadowing and same-scope duplicate detection. It depends only on sym.
package scope

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/sym"
)

// Three pairwise distinct failure modes, comparable with errors.Is.
var (
	ErrDuplicate  = errors.New("duplicate declaration in current scope")
	ErrUndeclared = errors.New("name is not declared")
	ErrExitGlobal = errors.New("cannot exit the global scope")
)

// Stack is a stack of scopes; frames[0] is the global scope.
type Stack struct {
	mu     sync.RWMutex
	frames []*sym.Scope
	// probes counts bindings inspected by the last Lookup (one map probe per
	// scanned frame) or duplicate check (one). Unexported; no exported method
	// exposes it (same-package tests read it directly).
	probes atomic.Int64
}

// New returns a Stack containing only the global scope.
func New() *Stack { return &Stack{frames: []*sym.Scope{sym.NewScope()}} }

// Enter pushes a fresh empty scope.
func (s *Stack) Enter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frames = append(s.frames, sym.NewScope())
}

// Exit pops the current scope, discarding all its bindings; with only the
// global scope left it returns ErrExitGlobal and changes nothing.
func (s *Stack) Exit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) == 1 {
		return ErrExitGlobal
	}
	s.frames[len(s.frames)-1] = nil // drop reference to discarded map
	s.frames = s.frames[:len(s.frames)-1]
	return nil
}

// Declare binds name -> t in the current scope. A name already present there
// returns ErrDuplicate and keeps the first binding; an outer same-name is a
// shadow and is allowed.
func (s *Stack) Declare(name string, t sym.T) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	top := s.frames[len(s.frames)-1]
	s.probes.Store(1) // one hash lookup performs the duplicate check
	if _, ok := top.Get(name); ok {
		return ErrDuplicate
	}
	top.Put(name, t)
	return nil
}

// Lookup returns the nearest binding of name, scanning innermost outward; it
// returns ErrUndeclared when no scope binds the name.
func (s *Stack) Lookup(name string) (sym.T, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	for i := len(s.frames) - 1; i >= 0; i-- {
		n++ // one map probe in this frame, however many names it holds
		if t, ok := s.frames[i].Get(name); ok {
			s.probes.Store(n)
			return t, nil
		}
	}
	s.probes.Store(n)
	return "", ErrUndeclared
}

// String renders the stack bottom->top as [{name:t ...} ...]. It takes a
// read lock and never exposes the probe counter.
func (s *Stack) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	for i, f := range s.frames {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteByte('{')
		for j, n := range f.Names() {
			if j > 0 {
				b.WriteByte(' ')
			}
			t, _ := f.Get(n)
			fmt.Fprintf(&b, "%s:%s", n, t)
		}
		b.WriteByte('}')
	}
	return b.String()
}

// CheckProbeBudget fills one scope with m names (m across several sizes) and
// asserts an existing-name Lookup inspects no more than one binding per hit,
// i.e. location is by hash map rather than linear scan. It reports only a
// pass/fail error; the probe count itself is never returned. It resets s.
func (s *Stack) CheckProbeBudget() error {
	for _, m := range []int{100, 1000, 10000} {
		f := sym.NewScope()
		for i := 0; i < m; i++ {
			f.Put(fmt.Sprintf("n%d", i), sym.Int)
		}
		s.mu.Lock()
		s.frames = []*sym.Scope{sym.NewScope(), f}
		s.mu.Unlock()
		target := fmt.Sprintf("n%d", m-1)
		if _, err := s.Lookup(target); err != nil {
			return err
		}
		if p := s.probes.Load(); p > 1 {
			return fmt.Errorf("m=%d Lookup scanned bindings instead of O(1) map lookup", m)
		}
	}
	s.reset()
	return nil
}
