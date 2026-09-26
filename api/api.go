// Package api is the external entry point to the scoped symbol table. It
// depends only on scope (which in turn depends on sym); the dependency
// direction api -> scope -> sym is strictly one way.
package api

import (
	"ontology/scope"
	"ontology/sym"
)

// Table is a nested-scope symbol table held in process memory.
type Table struct {
	s *scope.Stack
}

// The three pairwise distinct, decidable failure modes (re-exported from
// scope so callers depend only on api).
var (
	ErrDuplicate  = scope.ErrDuplicate
	ErrUndeclared = scope.ErrUndeclared
	ErrExitGlobal = scope.ErrExitGlobal
)

// New returns a table containing only the global scope.
func New() *Table { return &Table{s: scope.New()} }

// Enter pushes a fresh empty scope.
func (t *Table) Enter() { t.s.Enter() }

// Exit pops the current scope, discarding its bindings; it refuses to pop the
// global scope.
func (t *Table) Exit() error { return t.s.Exit() }

// Declare binds name -> ty in the current scope, rejecting a same-scope
// duplicate without overwriting it.
func (t *Table) Declare(name string, ty sym.T) error { return t.s.Declare(name, ty) }

// Lookup returns the nearest binding of name, innermost scope first.
func (t *Table) Lookup(name string) (sym.T, error) { return t.s.Lookup(name) }

// String renders the stack bottom->top; it never exposes internal counters.
func (t *Table) String() string { return t.s.String() }

// SelfCheck verifies, via built-in operation sequences, the four invariants
// (naive-reference agreement, shadow-without-overwrite, duplicate-keeps-first,
// no-trace-on-failure) and the O(1) lookup budget. It returns a descriptive
// sentinel-wrapped error on the first violation and resets the table.
func (t *Table) SelfCheck() error {
	if err := t.s.SelfCheck(); err != nil {
		return err
	}
	return t.s.CheckProbeBudget()
}
