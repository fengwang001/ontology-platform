// Package api is the external facade over the scope stack. The
// dependency direction api -> scope -> sym is strictly one way.
package api

import (
	"sort"

	"ontology/scope"
	"ontology/sym"
)

// T is the binding type; its definition lives in sym.
type T = sym.T

const (
	Int  = sym.Int
	Bool = sym.Bool
)

// Distinct sentinel errors callers judge failures with.
var (
	ErrDuplicate  = scope.ErrDuplicate
	ErrUndeclared = scope.ErrUndeclared
	ErrExitGlobal = scope.ErrExitGlobal
)

// Table is the nested-scope symbol table.
type Table struct{ st *scope.Stack }

// New returns a table whose stack contains only the global scope.
func New() *Table { return &Table{st: scope.New()} }

// Enter pushes a fresh scope.
func (t *Table) Enter() { t.st.Enter() }

// Exit pops the current scope; popping the global scope is rejected.
func (t *Table) Exit() error { return t.st.Exit() }

// Declare binds name to ty in the current scope only.
func (t *Table) Declare(name string, ty T) error { return t.st.Declare(name, ty) }

// Lookup returns the nearest binding of name from the inside out.
func (t *Table) Lookup(name string) (T, error) { return t.st.Lookup(name) }

// SelfCheck verifies the four invariants and the constant-probe guarantee.
func (t *Table) SelfCheck() error { return t.st.SelfCheck() }

// RenderStack renders the stack bottom-to-top for display.
func (t *Table) RenderStack() string {
	layers := t.st.Snapshot()
	out := "["
	for li, m := range layers {
		if li > 0 {
			out += "|"
		}
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		out += "{"
		for i, n := range names {
			if i > 0 {
				out += ","
			}
			out += n + ":" + m[n].String()
		}
		out += "}"
	}
	return out + "]"
}
